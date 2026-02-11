package core

import (
	"context"
	"errors"
)

type IService interface {
	Init(
		ctx context.Context,
	) error
	Cleanup() error
	Reset() error
}

type IHandler interface {
	Initialize(
		InputChan <-chan *EventPacket,
		outputChan chan<- *EventPacket,
		OutputTopChan chan<- *EventPacket,
		ctx context.Context,
	) error // Initializes the service with the given configuration.
	Start() error // Starts the service's main logic. This is where the service begins processing events.
	HandleEvent(packet *EventPacket) error

	Cleanup() error // Cleans up resources used by the service.
	Reset() error   // Resets the handler to its initial state.
}

type BaseHandler struct {
	Service               IService
	BackupServices        []IService
	Ctx                   context.Context
	InputChan             <-chan *EventPacket
	outputNextChan        chan<- *EventPacket
	outputTopChan         chan<- *EventPacket
	FatalServiceErrorChan chan error
	HandlerErrorChan      chan error
}

func (h *BaseHandler) Initialize(
	InputChan <-chan *EventPacket,
	OutputNextChan chan<- *EventPacket,
	OutputTopChan chan<- *EventPacket,
	ctx context.Context,
) error {
	h.InputChan = InputChan
	h.outputNextChan = OutputNextChan
	h.outputTopChan = OutputTopChan
	h.FatalServiceErrorChan = make(chan error)
	h.Ctx = ctx
	go h.fatalErrorHandlerLoop() // Start the fatal error handler loop in a separate goroutine.
	return h.Service.Init(ctx)
}

func (h *BaseHandler) Cleanup() error {
	return h.Service.Cleanup()
}

func (h *BaseHandler) Reset() error {
	return h.Service.Reset()
}

func (h *BaseHandler) SwitchToBackupService() error {
	if len(h.BackupServices) == 0 {
		return errors.New("no backup services available")
	}
	// Switch to the next backup service in the list.
	h.Service = h.BackupServices[0]
	// Re-initialize the new service with the existing context.
	if err := h.Service.Init(h.Ctx); err != nil {
		return err
	}
	h.BackupServices = h.BackupServices[1:] // Remove the switched service from the backup list.
	return nil
}

func (h *BaseHandler) SendPacket(packet *EventPacket) {
	switch packet.Destination {
	case EventRelayDestinationNextService:
		h.outputNextChan <- packet
	case EventRelayDestinationTopService:
		h.outputTopChan <- packet
	default:
		// Default to sending to the next service if destination is unrecognized.
		h.outputNextChan <- packet
	}
}

func (h *BaseHandler) HandleError(err error) {
	h.FatalServiceErrorChan <- err
}

func (h *BaseHandler) fatalErrorHandlerLoop() {
	for {
		select {
		case err := <-h.FatalServiceErrorChan:
			// Log the error (this is a placeholder, replace with actual logging).
			// log.Printf("Fatal error in handler: %v", err)
			// Attempt to switch to a backup service.
			if switchErr := h.SwitchToBackupService(); switchErr != nil {
				// If switching fails, log the error and exit the loop.
				// log.Printf("Failed to switch to backup service: %v", switchErr)
				return
			}
			h.SendPacket(
				NewEventPacket(&CriticalErrorEvent{Error: err.Error()}, EventRelayDestinationTopService, "BaseHandler"),
			)
		case <-h.Ctx.Done():
			return
		}
	}
}

func NewBaseHandler(service IService, backupServices []IService, ctx context.Context) *BaseHandler {
	return &BaseHandler{
		Service:        service,
		BackupServices: backupServices,
	}
}
