package core

import (
	"context"
	"errors"
)

type IService interface {
	Initialize(
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
	Logger                *Logger
	Ctx                   context.Context
	InputChan             <-chan *EventPacket
	outputNextChan chan<- *EventPacket
	outputTopChan  chan<- *EventPacket
	FatalServiceErrorChan chan error
	HandlerErrorChan      chan error
	handleEventFunc       func(packet *EventPacket) error // Function pointer to the actual handler's HandleEvent
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
	h.FatalServiceErrorChan = make(chan error, 100)
	h.HandlerErrorChan = make(chan error, 100)
	h.Ctx = ctx
	go h.fatalErrorHandlerLoop() // Start the fatal error handler loop in a separate goroutine.
	go h.handlerErrorLoop()      // Start handler error loop
	// Note: HandlerLoop is started in SetHandleEventFunc to ensure the callback is set
	return h.Service.Initialize(ctx)
}

func (h *BaseHandler) Cleanup() error {
	h.Logger.Infof("Cleaning up service: %T", h.Service)
	// Close error channels to signal goroutines to stop
	if h.FatalServiceErrorChan != nil {
		close(h.FatalServiceErrorChan)
		h.FatalServiceErrorChan = nil
	}
	if h.HandlerErrorChan != nil {
		close(h.HandlerErrorChan)
		h.HandlerErrorChan = nil
	}
	return h.Service.Cleanup()
}

func (h *BaseHandler) Reset() error {
	return h.Service.Reset()
}

// WithBackupService registers a fallback service used when the primary fails.
// Returns the handler to allow chaining.
func (h *BaseHandler) WithBackupService(service IService) *BaseHandler {
	h.BackupServices = append(h.BackupServices, service)
	return h
}

func (h *BaseHandler) SwitchToBackupService() error {
	if len(h.BackupServices) == 0 {
		return errors.New("no backup services available")
	}
	// Switch to the next backup service in the list.
	h.Service = h.BackupServices[0]
	// Re-initialize the new service with the existing context.
	if err := h.Service.Initialize(h.Ctx); err != nil {
		return err
	}
	h.BackupServices = h.BackupServices[1:] // Remove the switched service from the backup list.
	return nil
}

func (h *BaseHandler) SendPacket(packet *EventPacket) {
	// Check if context is done first
	select {
	case <-h.Ctx.Done():
		return
	default:
	}

	switch packet.Destination {
	case EventRelayDestinationNextService:
		if h.outputNextChan == nil {
			return
		}
		select {
		case h.outputNextChan <- packet:
		case <-h.Ctx.Done():
			return
		}

	case EventRelayDestinationTopService:
		if h.outputTopChan == nil {
			return
		}
		select {
		case h.outputTopChan <- packet:
		case <-h.Ctx.Done():
			return
		}

	default:
		if h.outputNextChan == nil {
			return
		}
		select {
		case h.outputNextChan <- packet:
		case <-h.Ctx.Done():
			return
		default:
			h.Logger.Warnf("Output channel is blocked, dropping packet: %v", packet)
		}
	}
}

func (h *BaseHandler) HandleError(err error) {
	h.FatalServiceErrorChan <- err
}

func (h *BaseHandler) HandleEvent(eventPacket *EventPacket) error {
	// Default implementation does nothing. Override in specific handlers.
	return errors.New("HandleEvent not implemented")
}

// SetHandleEventFunc sets the callback for handling events and starts the handler loop.
// This should be called by embedding handlers after Initialize to set their own HandleEvent method.
func (h *BaseHandler) SetHandleEventFunc(fn func(packet *EventPacket) error) {
	h.handleEventFunc = fn
	go h.HandlerLoop()
}

func (h *BaseHandler) HandlerLoop() {
	for {
		select {
		case eventPacket := <-h.InputChan:
			if h.handleEventFunc != nil {
				select { // Check if context is done before processing the event
				case <-h.Ctx.Done():
					return
				default:
				}
				if err := h.handleEventFunc(eventPacket); err != nil {
					select {
					case h.HandlerErrorChan <- err:
					case <-h.Ctx.Done():
						return
					default:
						// Channel closed or full, log directly
						h.Logger.Errorf("Handler error: %v", err)
					}
				}
			} else if err := h.HandleEvent(eventPacket); err != nil {
				select {
				case h.HandlerErrorChan <- err:
				case <-h.Ctx.Done():
					return
				default:
					h.Logger.Errorf("Handler error: %v", err)
				}
			}
		case <-h.Ctx.Done():
			return
		}
	}
}

func (h *BaseHandler) fatalErrorHandlerLoop() {
	for {
		select {
		case err, ok := <-h.FatalServiceErrorChan:
			if !ok {
				// Channel closed, exit gracefully
				return
			}
			h.Logger.Errorf("Fatal error in handler: %v", err)
			// Attempt to switch to a backup service.
			if switchErr := h.SwitchToBackupService(); switchErr != nil {
				// If switching fails, log the error and exit the loop.
				h.SendPacket(
					NewEventPacket(&CriticalErrorEvent{Error: err.Error()}, EventRelayDestinationTopService, "BaseHandler"),
				)
				h.Logger.Errorf("Failed to switch to backup service: %v", switchErr)
				return
			}
		case <-h.Ctx.Done():
			return
		}
	}
}

func (h *BaseHandler) handlerErrorLoop() {
	for {
		select {
		case err, ok := <-h.HandlerErrorChan:
			if !ok {
				// Channel closed, exit gracefully
				return
			}
			// Log handler errors but don't treat them as fatal
			h.Logger.Errorf("Handler error: %v", err)
		case <-h.Ctx.Done():
			return
		}
	}
}

func NewBaseHandler(service IService, backupServices []IService, ctx context.Context, logger *Logger) *BaseHandler {
	if logger == nil {
		logger = GetLogger()
	}
	return &BaseHandler{
		Service:        service,
		BackupServices: backupServices,
		Logger:         logger,
	}
}
