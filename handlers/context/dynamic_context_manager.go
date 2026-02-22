package context

import (
	"fmt"
	"strings"
	"sync"

	"interactkit/core"
	"interactkit/events/task"
)

// DynamicContextManager layers task-group-driven context management on top of
// LLMContextManager. It controls which system instructions and tools the LLM
// sees at any point in the conversation and completes tasks automatically
// as the LLM calls set_<varName> tools to collect required information.
//
// # How it works
//
//  1. Call LoadTaskGroup with a TaskGroupConfig.
//     The manager inserts a system message right after the base system messages
//     and wires up set_<varName> tool handlers for every declared variable.
//
//  2. The LLM sees ALL currently visible tasks' instructions together, plus
//     the union of their uncollected set_* variable tools (and global / per-task
//     tools). The visible set expands as tasks complete and dependencies unlock.
//
//  3. As the LLM calls set_<varName>("value"), the manager validates and stores
//     the value against whichever visible task owns that variable. Once ALL
//     variables for that task are collected the task is marked complete, newly
//     visible tasks are merged into the instruction message and tool set, and the
//     existing HandleToolCall mechanism in LLMAssistantContextAggregator fires a
//     new LLMGenerateResponseEvent — no external coordination required.
//
// Variable names should be unique across the task group. If the same name
// appears in two visible tasks the handler resolves against the first visible
// task (by config order) that owns the variable.
type DynamicContextManager struct {
	*LLMContextManager

	config         *TaskGroupConfig
	permanentTools []core.LLMTool          // tools present before LoadTaskGroup (e.g. continue_listening)
	toolRegistry   map[string]core.LLMTool // tools registered via RegisterTool

	completedTasks map[string]bool
	collectedVars  map[string]map[string]string // taskID -> varName -> value
	taskInstrIndex int                          // stable index in context.Messages for the task instruction msg

	mu sync.Mutex
}

// NewDynamicContextManager wraps an existing LLMContextManager.
// Call LoadTaskGroup to activate task-group-driven context management.
func NewDynamicContextManager(base *LLMContextManager) *DynamicContextManager {
	return &DynamicContextManager{
		LLMContextManager: base,
		toolRegistry:      make(map[string]core.LLMTool),
		completedTasks:    make(map[string]bool),
		collectedVars:     make(map[string]map[string]string),
	}
}

// RegisterTool makes a named tool available for task groups to reference in
// global_tools and visible_tool_ids. Call this before LoadTaskGroup.
func (d *DynamicContextManager) RegisterTool(tool core.LLMTool, handler ToolHandlerFunc) {
	d.mu.Lock()
	d.toolRegistry[tool.ToolId] = tool
	d.mu.Unlock()
	d.LLMContextManager.RegisterToolHandler(tool.ToolId, handler)
}

// LoadTaskGroup installs a task group and begins dynamic context management.
// It inserts the initial tasks' instructions as a system message and registers
// set_<varName> handlers for every variable across all tasks.
//
// LoadTaskGroup must be called after SetContext so that permanent tools
// (e.g. continue_listening) are already present in context.Tools.
func (d *DynamicContextManager) LoadTaskGroup(config *TaskGroupConfig) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.findTaskLocked(config, config.InitialTaskID) == nil {
		return fmt.Errorf("dynamicContextManager: initial task %q not found in config", config.InitialTaskID)
	}

	d.config = config
	d.completedTasks = make(map[string]bool)
	d.collectedVars = make(map[string]map[string]string)

	ctx := d.LLMContextManager.context
	if ctx == nil {
		ctx = &core.LLMContext{}
		d.LLMContextManager.context = ctx
	}

	// Snapshot tools that exist before we take over so they are always
	// restored as "permanent" tools (e.g. continue_listening, end_call).
	d.permanentTools = make([]core.LLMTool, len(ctx.Tools))
	copy(d.permanentTools, ctx.Tools)

	// Find insertion point: directly after the last consecutive leading system message.
	insertIdx := 0
	for i, msg := range ctx.Messages {
		if msg.Role == core.LLMMessageRoleSystem {
			insertIdx = i + 1
		} else {
			break
		}
	}

	visible := d.visibleTasksLocked()
	taskMsg := core.LLMMessage{
		Role:    core.LLMMessageRoleSystem,
		Message: d.buildTaskInstructionsLocked(visible),
	}
	newMessages := make([]core.LLMMessage, 0, len(ctx.Messages)+1)
	newMessages = append(newMessages, ctx.Messages[:insertIdx]...)
	newMessages = append(newMessages, taskMsg)
	newMessages = append(newMessages, ctx.Messages[insertIdx:]...)
	ctx.Messages = newMessages
	d.taskInstrIndex = insertIdx

	d.rebuildToolsLocked(visible)
	d.registerVariableHandlersLocked(config)

	ids := make([]string, len(visible))
	for i, t := range visible {
		ids[i] = t.ID
	}
	d.Logger.Infof(
		"DynamicContextManager: loaded task group %q — initial visible tasks: [%s]",
		config.Namespace, strings.Join(ids, ", "),
	)
	return nil
}

// CompleteCurrentTask manually marks the first visible task complete and
// recalculates the active task set. Useful for dialogue-only tasks (no
// variables) where completion must be triggered programmatically.
func (d *DynamicContextManager) CompleteCurrentTask() {
	d.mu.Lock()
	defer d.mu.Unlock()
	visible := d.visibleTasksLocked()
	if len(visible) == 0 {
		return
	}
	d.onTaskCompleteLocked(visible[0].ID)
}

// CollectedVars returns a deep copy of all collected variable values,
// keyed by taskID and then variable name.
func (d *DynamicContextManager) CollectedVars() map[string]map[string]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string]map[string]string, len(d.collectedVars))
	for tid, vars := range d.collectedVars {
		cp := make(map[string]string, len(vars))
		for k, v := range vars {
			cp[k] = v
		}
		out[tid] = cp
	}
	return out
}

// CurrentTaskID returns the ID of the first visible incomplete task,
// or an empty string when all tasks are complete.
func (d *DynamicContextManager) CurrentTaskID() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	visible := d.visibleTasksLocked()
	if len(visible) == 0 {
		return ""
	}
	return visible[0].ID
}

// IsComplete returns true when there are no visible incomplete tasks remaining.
func (d *DynamicContextManager) IsComplete() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.config == nil {
		return false
	}
	return len(d.visibleTasksLocked()) == 0
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal helpers — all methods named *Locked assume d.mu is already held.
// ─────────────────────────────────────────────────────────────────────────────

func (d *DynamicContextManager) registerVariableHandlersLocked(config *TaskGroupConfig) {
	registered := make(map[string]bool)
	for ti := range config.Tasks {
		for vi := range config.Tasks[ti].Variables {
			v := config.Tasks[ti].Variables[vi]
			toolID := "set_" + v.Name
			if registered[toolID] {
				continue
			}
			registered[toolID] = true

			// Register the tool handler.
			captured := v.Name
			d.LLMContextManager.RegisterToolHandler(toolID, func(call core.LLMToolCall) string {
				return d.handleSetVariable(captured, call)
			})

			// Register metadata so HandleToolCall can generate filler/confirm phrases.
			d.LLMContextManager.registerVariableTool(toolID, VariableToolInfo{
				Description:    v.Description,
				AutoConfirm:    v.AutoConfirm,
				ConfirmPhrases: v.ConfirmPhrases,
			})
		}
	}
}

// handleSetVariable is invoked when the LLM calls set_<varName>.
// It resolves the variable against the first visible task that owns it,
// validates and stores the value, and marks that task complete when all
// its variables have been collected.
func (d *DynamicContextManager) handleSetVariable(varName string, call core.LLMToolCall) string {
	value := extractParam(call, "value")

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.config == nil {
		return "No task group is currently loaded."
	}

	// Find the first visible task that owns this variable.
	var ownerTask *TaskDef
	var varDef *TaskVariable
	for _, task := range d.visibleTasksLocked() {
		for i := range task.Variables {
			if task.Variables[i].Name == varName {
				ownerTask = task
				varDef = &task.Variables[i]
				break
			}
		}
		if ownerTask != nil {
			break
		}
	}
	if ownerTask == nil {
		return fmt.Sprintf("Variable %q is not expected in any currently active task.", varName)
	}

	if !varDef.Validate(value) {
		return d.validationMsgLocked(varName, varDef, value)
	}

	taskID := ownerTask.ID
	if d.collectedVars[taskID] == nil {
		d.collectedVars[taskID] = make(map[string]string)
	}
	d.collectedVars[taskID][varName] = value

	d.Logger.With(map[string]any{
		"namespace":   d.config.Namespace,
		"task_id":     taskID,
		"variable":    varName,
		"value":       value,
		"autoconfirm": varDef.AutoConfirm,
	}).Info("variable captured")
	d.Logger.Infof("DynamicContextManager [%s/%s]: captured %s=%q — task vars so far: %v",
		d.config.Namespace, taskID, varName, value, d.collectedVars[taskID],
	)

	// Rebuild tools to remove the just-collected variable's set_* tool.
	d.rebuildToolsLocked(d.visibleTasksLocked())

	if d.allVarsCollectedLocked(ownerTask) {
		taskName := ownerTask.Name
		d.onTaskCompleteLocked(taskID)
		return fmt.Sprintf(
			"Captured %s=%q. All variables for %q are now collected — proceeding to the next step.",
			varName, value, taskName,
		)
	}

	// Report remaining variables for this task.
	collected := d.collectedVars[taskID]
	var pending []string
	for _, v := range ownerTask.Variables {
		if _, ok := collected[v.Name]; !ok {
			pending = append(pending, v.Name)
		}
	}
	return fmt.Sprintf("Captured %s=%q. Still need for %q: %s.", varName, value, ownerTask.Name, strings.Join(pending, ", "))
}

// onTaskCompleteLocked marks the given task complete, recalculates the full
// visible task set, and updates the system message and tool list accordingly.
// Must be called with d.mu held.
func (d *DynamicContextManager) onTaskCompleteLocked(taskID string) {
	if d.config == nil {
		return
	}

	prevTask := d.findTaskLocked(d.config, taskID)
	d.completedTasks[taskID] = true

	d.Logger.With(map[string]any{
		"namespace": d.config.Namespace,
		"task_id":   taskID,
		"task_name": func() string {
			if prevTask != nil {
				return prevTask.Name
			}
			return taskID
		}(),
	}).Info("task step completed")

	if vars := d.collectedVars[taskID]; len(vars) > 0 {
		fields := make(map[string]any, len(vars)+2)
		fields["namespace"] = d.config.Namespace
		fields["task_id"] = taskID
		for k, v := range vars {
			fields["var."+k] = v
		}
		d.Logger.With(fields).Info("collected variables")
	} else {
		d.Logger.With(map[string]any{
			"namespace": d.config.Namespace,
			"task_id":   taskID,
		}).Info("no variables collected for this step (dialogue-only task)")
	}

	// Emit a TaskVariablesEvent for this task's completion.
	d.emitTaskEventLocked(taskID, false)

	nextVisible := d.visibleTasksLocked()
	if len(nextVisible) == 0 {
		d.Logger.With(map[string]any{
			"namespace": d.config.Namespace,
		}).Info("all tasks in group completed — session is done")

		d.updateTaskInstructionLocked(
			"You have everything you need. Wrap up the conversation warmly and naturally.",
		)
		d.clearDynamicToolsLocked()

		allFields := map[string]any{"namespace": d.config.Namespace}
		for _, t := range d.config.Tasks {
			for k, v := range d.collectedVars[t.ID] {
				allFields[t.ID+"."+k] = v
			}
		}
		d.Logger.With(allFields).Info("session summary — all collected variables")
		d.emitTaskEventLocked(taskID, true)
		return
	}

	d.updateTaskInstructionLocked(d.buildTaskInstructionsLocked(nextVisible))
	d.rebuildToolsLocked(nextVisible)

	nextIDs := make([]string, len(nextVisible))
	for i, t := range nextVisible {
		nextIDs[i] = t.ID
	}
	d.Logger.With(map[string]any{
		"namespace":      d.config.Namespace,
		"completed_task": taskID,
		"visible_tasks":  strings.Join(nextIDs, ", "),
	}).Info("task completed — visible task set updated")
}

// rebuildToolsLocked replaces context.Tools with the correct set for the given
// visible tasks: permanent tools + global tools + task-visible tools +
// set_* variable tools for all uncollected variables across all visible tasks.
// Must be called with d.mu held.
func (d *DynamicContextManager) rebuildToolsLocked(tasks []*TaskDef) {
	ctx := d.LLMContextManager.context
	if ctx == nil {
		return
	}

	tools := make([]core.LLMTool, 0, len(d.permanentTools)+len(d.config.GlobalTools))

	// 1. Always-on tools (e.g. continue_listening, end_call).
	tools = append(tools, d.permanentTools...)

	// 2. Global tools visible during every task.
	for _, id := range d.config.GlobalTools {
		if t, ok := d.toolRegistry[id]; ok {
			tools = append(tools, t)
		}
	}

	seen := make(map[string]bool)
	for _, task := range tasks {
		// 3. Task-specific visible tools.
		for _, id := range task.VisibleToolIDs {
			if !seen[id] {
				if t, ok := d.toolRegistry[id]; ok {
					tools = append(tools, t)
					seen[id] = true
				}
			}
		}

		// 4. Auto-generated set_* tools for variables not yet collected.
		collected := d.collectedVars[task.ID]
		for _, v := range task.Variables {
			if _, done := collected[v.Name]; done {
				continue
			}
			toolID := "set_" + v.Name
			if !seen[toolID] {
				tools = append(tools, d.makeSetTool(v))
				seen[toolID] = true
			}
		}
	}

	ctx.Tools = tools
}

// clearDynamicToolsLocked resets the tool list to permanent-only tools.
// Must be called with d.mu held.
func (d *DynamicContextManager) clearDynamicToolsLocked() {
	ctx := d.LLMContextManager.context
	if ctx == nil {
		return
	}
	ctx.Tools = make([]core.LLMTool, len(d.permanentTools))
	copy(ctx.Tools, d.permanentTools)
}

// updateTaskInstructionLocked replaces the content of the task-instruction
// system message in context.Messages. Must be called with d.mu held.
func (d *DynamicContextManager) updateTaskInstructionLocked(text string) {
	ctx := d.LLMContextManager.context
	if ctx == nil || d.taskInstrIndex >= len(ctx.Messages) {
		return
	}
	ctx.Messages[d.taskInstrIndex].Message = text
}

// visibleTasksLocked returns all tasks that are currently in scope:
// their VisibleAfterTaskID dependency is satisfied, they are not completed,
// and they have not disappeared. Order follows config order.
// Must be called with d.mu held.
func (d *DynamicContextManager) visibleTasksLocked() []*TaskDef {
	var visible []*TaskDef
	for i := range d.config.Tasks {
		task := &d.config.Tasks[i]
		if d.completedTasks[task.ID] {
			continue
		}
		if !d.isVisibleLocked(task) || d.isDisappearedLocked(task) {
			continue
		}
		visible = append(visible, task)
	}
	return visible
}

func (d *DynamicContextManager) isVisibleLocked(task *TaskDef) bool {
	return task.VisibleAfterTaskID == "" || d.completedTasks[task.VisibleAfterTaskID]
}

func (d *DynamicContextManager) isDisappearedLocked(task *TaskDef) bool {
	return task.DisappearAfterTaskID != "" && d.completedTasks[task.DisappearAfterTaskID]
}

func (d *DynamicContextManager) allVarsCollectedLocked(task *TaskDef) bool {
	if len(task.Variables) == 0 {
		return true
	}
	collected := d.collectedVars[task.ID]
	for _, v := range task.Variables {
		if _, ok := collected[v.Name]; !ok {
			return false
		}
	}
	return true
}

func (d *DynamicContextManager) findTaskLocked(config *TaskGroupConfig, id string) *TaskDef {
	for i := range config.Tasks {
		if config.Tasks[i].ID == id {
			return &config.Tasks[i]
		}
	}
	return nil
}

// buildTaskInstructionsLocked constructs the system message for all currently
// visible tasks. A single visible task gets a focused "right now" directive.
// Multiple visible tasks are listed together so the LLM can collect information
// for any of them as the conversation progresses.
// Must be called with d.mu held.
func (d *DynamicContextManager) buildTaskInstructionsLocked(tasks []*TaskDef) string {
	if len(tasks) == 0 {
		return "You have everything you need. Wrap up the conversation warmly and naturally."
	}

	var sb strings.Builder

	if len(tasks) == 1 {
		sb.WriteString("Focus on the following right now:\n\n")
		sb.WriteString(tasks[0].Instructions)
		sb.WriteString(d.buildVarConstraintsLocked(tasks[0]))
	} else {
		sb.WriteString("You are working through the following topics simultaneously. " +
			"Collect the required information for each as the conversation progresses — " +
			"complete each topic fully before moving on.\n\n")
		for i, task := range tasks {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, task.Name))
			sb.WriteString(task.Instructions)
			sb.WriteString(d.buildVarConstraintsLocked(task))
			if i < len(tasks)-1 {
				sb.WriteString("\n\n")
			}
		}
	}

	return sb.String()
}

// buildVarConstraintsLocked returns the constraint hints and closing directive
// for a single task's variables. Must be called with d.mu held.
func (d *DynamicContextManager) buildVarConstraintsLocked(task *TaskDef) string {
	if len(task.Variables) == 0 {
		return "\nComplete this part of the conversation before moving on to anything else."
	}

	var sb strings.Builder
	sb.WriteString("\n")
	for _, v := range task.Variables {
		switch {
		case len(v.Enum) > 0:
			human := make([]string, len(v.Enum))
			for i, e := range v.Enum {
				human[i] = strings.ReplaceAll(e, "_", " ")
			}
			sb.WriteString(fmt.Sprintf(
				"\nFor %s, the value must be one of: %s.",
				v.Description, strings.Join(human, ", "),
			))
		case v.Example != "":
			sb.WriteString(fmt.Sprintf(
				"\nFor %s, a typical value looks like: %s.",
				v.Description, v.Example,
			))
		}
	}
	sb.WriteString("\nRecord the information using the tools available as soon as you have it. Do not ask about other topics or move the conversation forward until everything needed here is captured.")
	return sb.String()
}

// makeSetTool builds the LLMTool definition for a variable's set_* tool.
func (d *DynamicContextManager) makeSetTool(v TaskVariable) core.LLMTool {
	toolID := "set_" + v.Name
	desc := v.Description
	paramDesc := v.Description

	if len(v.Enum) > 0 {
		human := make([]string, len(v.Enum))
		for i, e := range v.Enum {
			human[i] = strings.ReplaceAll(e, "_", " ")
		}
		paramDesc = fmt.Sprintf("%s (one of: %s)", v.Description, strings.Join(human, ", "))
	} else if v.Example != "" {
		paramDesc = fmt.Sprintf("%s (e.g. %s)", v.Description, v.Example)
	}

	return core.LLMTool{
		ToolId:      toolID,
		Name:        toolID,
		Description: desc,
		Parameters: []core.Parameter{
			{
				Name:        "value",
				Description: paramDesc,
				Required:    true,
				Type:        core.LLMParameterTypeString,
				Example:     v.Example,
			},
		},
	}
}

// emitTaskEventLocked fires a TaskVariablesEvent through the pipeline.
// Must be called with d.mu held; spawns a goroutine to avoid blocking.
func (d *DynamicContextManager) emitTaskEventLocked(taskID string, isFinal bool) {
	emitter := d.LLMContextManager.eventEmitter
	if emitter == nil {
		return
	}
	flat := make(map[string]string)
	for _, vars := range d.collectedVars {
		for k, v := range vars {
			flat[k] = v
		}
	}
	ev := &task.TaskVariablesEvent{
		Namespace: d.config.Namespace,
		TaskID:    taskID,
		IsFinal:   isFinal,
		Variables: flat,
	}
	go emitter(ev)
}

func (d *DynamicContextManager) validationMsgLocked(varName string, v *TaskVariable, value string) string {
	if len(v.Enum) > 0 {
		return fmt.Sprintf(
			"Invalid value %q for %s. Please choose one of: %s.",
			value, varName, strings.Join(v.Enum, ", "),
		)
	}
	if v.Regex != "" {
		return fmt.Sprintf(
			"Invalid value %q for %s. It must match the pattern: %s.",
			value, varName, v.Regex,
		)
	}
	return fmt.Sprintf("Invalid value %q for %s. Please provide a non-empty value.", value, varName)
}

// extractParam pulls a string parameter from an LLMToolCall by key.
func extractParam(call core.LLMToolCall, key string) string {
	if call.Parameters == nil {
		return ""
	}
	v, ok := (*call.Parameters)[key]
	if !ok {
		return ""
	}
	return fmt.Sprintf("%v", v)
}
