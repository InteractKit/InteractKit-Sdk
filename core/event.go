package core

type IEvent interface {
	GetId() string // Returns the unique identifier of the event.
}

// IExternalOutputEvent is implemented by events that should be routed to the
// ExternalEventHandler instead of flowing up or down the pipeline.
type IExternalOutputEvent interface {
	IEvent
}

// IExternalInputEvent is implemented by events that originate outside the
// pipeline. When the ExternalEventHandler receives one, it is pushed to the
// pipeline top so all handlers can observe it.
type IExternalInputEvent interface {
	IEvent
}
