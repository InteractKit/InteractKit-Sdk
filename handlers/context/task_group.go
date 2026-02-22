package context

import (
	"encoding/json"
	"regexp"
)

// TaskVariable is a piece of information the LLM must collect during a task.
// For each variable the system auto-generates a set_<Name> tool that the LLM
// calls when it has obtained the value from the user.
type TaskVariable struct {
	// Name is the identifier used to generate the set_<Name> tool.
	// Must be unique across the whole task group.
	Name string `json:"name"`

	// Description is shown to the LLM as the tool's description.
	Description string `json:"description"`

	// Regex is an optional Go regular-expression the value must satisfy.
	Regex string `json:"regex,omitempty"`

	// Enum is an optional allow-list; the value must be one of these strings.
	Enum []string `json:"enum,omitempty"`

	// Example is shown to the LLM as an example value in the tool definition.
	Example string `json:"example,omitempty"`

	// AutoConfirm instructs the system to read the captured value back to the
	// caller and ask them to verify it before continuing. Useful for values
	// that must match exactly (phone numbers, addresses, etc.) where a
	// mishearing would be costly. Typically paired with a Regex constraint.
	AutoConfirm bool `json:"autoconfirm,omitempty"`

	// ConfirmPhrases is an optional list of read-back + verification phrases
	// spoken when autoconfirm is true. Use {{value}} as the placeholder for
	// the captured value. When omitted the system falls back to its built-in
	// confirmation phrase pool.
	ConfirmPhrases []string `json:"confirm_phrases,omitempty"`
}

// Validate returns true when value passes all declared constraints.
// An empty Regex is treated as "anything non-empty".
func (v *TaskVariable) Validate(value string) bool {
	if len(v.Enum) > 0 {
		for _, allowed := range v.Enum {
			if allowed == value {
				return true
			}
		}
		return false
	}
	if v.Regex != "" {
		re, err := regexp.Compile(v.Regex)
		if err != nil {
			return true // malformed regex: accept anything
		}
		return re.MatchString(value)
	}
	return value != ""
}

// TaskDef describes one step in a task group.
type TaskDef struct {
	// ID is the unique identifier for this task within the group.
	ID string `json:"id"`

	// Name is a short human-readable label (shown to the LLM in the prompt).
	Name string `json:"name"`

	// Instructions is injected as a system message while this task is active.
	Instructions string `json:"instructions"`

	// Variables are the pieces of information that must be collected.
	// Completing all variables causes the system to advance to the next task.
	Variables []TaskVariable `json:"variables,omitempty"`

	// DisappearAfterTaskID: this task is skipped once the named task completes.
	DisappearAfterTaskID string `json:"disappear_after_task_id,omitempty"`

	// VisibleToolIDs lists extra tools (by ID) that should be visible while
	// this task is active. These must be pre-registered via RegisterTool.
	VisibleToolIDs []string `json:"visible_tool_ids,omitempty"`

	// VisibleAfterTaskID: this task is hidden until the named task completes.
	VisibleAfterTaskID string `json:"visible_after_task_id,omitempty"`
}

// TaskGroupConfig is the root descriptor for a dynamic task group.
type TaskGroupConfig struct {
	// Namespace is a unique identifier for this task group (for logging).
	Namespace string `json:"namespace"`

	// BaseInstructions are prepended to the initial context system message.
	// They describe the overall goal and are always visible to the LLM.
	BaseInstructions string `json:"base_instructions"`

	// InitialTaskID is the ID of the task to start with.
	InitialTaskID string `json:"initial_task_id"`

	// GlobalTools lists tool IDs that are visible during every task.
	// These must be pre-registered via RegisterTool.
	GlobalTools []string `json:"global_tools,omitempty"`

	// Tasks is the ordered list of all tasks in this group.
	Tasks []TaskDef `json:"tasks"`
}

// ParseTaskGroup unmarshals a JSON byte slice into a TaskGroupConfig.
func ParseTaskGroup(data []byte) (*TaskGroupConfig, error) {
	var cfg TaskGroupConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
