package core

type IEvent interface {
	GetId() string // Returns the unique identifier of the event.
}
