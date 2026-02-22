package core

type CriticalErrorEvent struct {
	Error string
}

func (e *CriticalErrorEvent) GetId() string {
	return "shared.critical_error"
}

type WarningEvent struct {
	Error string
}

func (e *WarningEvent) GetId() string {
	return "shared.warning"
}

// EndCallEvent is fired when the agent decides to terminate the session.
// The runner handles it by stopping the pipeline gracefully.
type EndCallEvent struct {
	Reason string
}

func (e *EndCallEvent) GetId() string {
	return "shared.end_call"
}
