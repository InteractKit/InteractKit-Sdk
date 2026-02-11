package core

import "github.com/google/uuid"

type EventRelayDestination int

const (
	EventRelayDestinationNextService EventRelayDestination = iota + 1 // Pass to the next service in the pipeline.
	EventRelayDestinationTopService                                   // Pass to the top-level service. This is useful for sending events to all services.
)

type EventPacket struct {
	Event       IEvent
	Destination EventRelayDestination
	Uid         string // Unique identifier for tracking the event packet.
	Relayer     string // Identifier of the service that relayed the event.
}

func NewEventPacket(event IEvent, destination EventRelayDestination, relayer string) *EventPacket {
	uid := uuid.New().String() // Generate a unique identifier for the event packet.
	return &EventPacket{
		Event:       event,
		Destination: destination,
		Uid:         uid,
		Relayer:     relayer,
	}
}
