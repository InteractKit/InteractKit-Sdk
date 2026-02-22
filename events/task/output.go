package task

// TaskVariablesEvent is emitted as an IExternalOutputEvent whenever a task
// completes (IsFinal=false) or all tasks in the group finish (IsFinal=true).
// Variables is a flat map of all collected variable values up to that point.
type TaskVariablesEvent struct {
	Namespace string            `json:"namespace"`
	TaskID    string            `json:"task_id"`
	IsFinal   bool              `json:"is_final"`
	Variables map[string]string `json:"variables"`
}

func (e *TaskVariablesEvent) GetId() string { return "task.variables_collected" }
