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
