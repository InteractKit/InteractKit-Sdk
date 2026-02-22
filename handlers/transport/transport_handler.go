package transport

import (
	"context"
	"interactkit/core"
	"interactkit/events/transport"
)

type ITransportService interface {
	core.IService
	Connect() error
	SendEvent(data core.IEvent) error
	StartReceiving(outputChan chan<- core.MediaChunk, errorChan chan<- error)
}

// TransportHandlerWrapper holds shared state
type TransportHandlerWrapper struct {
	service        ITransportService
	backupServices []ITransportService
	config         TransportConfig
	logger         *core.Logger

	// Shared state
	connected bool
}

// NewTransportHandlerWrapper creates a new transport handler wrapper.
// Use DefaultConfig() to get a config with sensible defaults and override only what you need.
// Chain WithBackupService to register fallback services.
func NewTransportHandlerWrapper(
	service ITransportService,
	config TransportConfig,
	logger *core.Logger,
) *TransportHandlerWrapper {
	return &TransportHandlerWrapper{
		service: service,
		config:  config,
		logger:  logger,
	}
}

// WithBackupService registers a fallback service used when the primary fails.
// Returns the wrapper to allow chaining.
func (w *TransportHandlerWrapper) WithBackupService(service ITransportService) *TransportHandlerWrapper {
	w.backupServices = append(w.backupServices, service)
	return w
}

func (w *TransportHandlerWrapper) GetInputHandler() *TransportInputHandler {
	typedServices := make([]core.IService, len(w.backupServices))
	for i, s := range w.backupServices {
		typedServices[i] = s
	}

	return &TransportInputHandler{
		BaseHandler: *core.NewBaseHandler(
			w.service, typedServices, nil, w.logger,
		),
		config:  w.config,
		wrapper: w,
	}
}

func (w *TransportHandlerWrapper) GetOutputHandler() *TransportOutputHandler {
	typedServices := make([]core.IService, len(w.backupServices))
	for i, s := range w.backupServices {
		typedServices[i] = s
	}

	return &TransportOutputHandler{
		BaseHandler: *core.NewBaseHandler(
			w.service, typedServices, nil, w.logger,
		),
		config:  w.config,
		wrapper: w,
	}
}

// TransportInputHandler handles incoming data
type TransportInputHandler struct {
	core.BaseHandler
	config  TransportConfig
	wrapper *TransportHandlerWrapper
}

func (h *TransportInputHandler) Initialize(
	inputChan <-chan *core.EventPacket,
	outputNextChan chan<- *core.EventPacket,
	outputTopChan chan<- *core.EventPacket,
	ctx context.Context,
) error {
	h.Logger.Info("Initializing TransportInputHandler with service")
	err := h.BaseHandler.Initialize(inputChan, outputNextChan, outputTopChan, ctx)
	if err != nil {
		return err
	}
	h.BaseHandler.SetHandleEventFunc(h.HandleEvent)

	if !h.wrapper.connected {
		err = h.Service.(ITransportService).Connect()
		if err != nil {
			h.Logger.With(map[string]interface{}{"error": err}).Error("Failed to connect transport service")
			return err
		}
		h.wrapper.connected = true
	}
	return nil
}

func (h *TransportInputHandler) Start() error {
	outputChan := make(chan core.MediaChunk, 100)
	errorChan := make(chan error, 10)

	go h.Service.(ITransportService).StartReceiving(outputChan, errorChan)

	for {
		select {
		case outputData := <-outputChan:
			// Process audio synchronously to avoid goroutine accumulation
			// The VAD has a mutex that would cause goroutine pileup if spawned async
			processAudio(outputData, h)

		case err := <-errorChan:
			h.FatalServiceErrorChan <- err
		case <-h.Ctx.Done():
			return nil
		}
	}
}

func processAudio(outputData core.MediaChunk, h *TransportInputHandler) {
	audioChunk, videoChunk, textChunk := outputData.Audio, outputData.Video, outputData.Text
	if audioChunk.Data != nil {

		audioEvent := &transport.TransportAudioInputEvent{
			AudioChunk: audioChunk,
		}
		h.SendPacket(core.NewEventPacket(audioEvent, core.EventRelayDestinationNextService, "TransportInputHandler"))
	}

	if videoChunk.Data != nil {
		videoEvent := &transport.TransportVideoInputEvent{
			VideoChunk: videoChunk,
		}
		h.SendPacket(core.NewEventPacket(videoEvent, core.EventRelayDestinationNextService, "TransportInputHandler"))
	}

	if textChunk.Text != "" {
		textEvent := &transport.TransportTextInputEvent{
			Text: textChunk.Text,
		}
		h.SendPacket(core.NewEventPacket(textEvent, core.EventRelayDestinationNextService, "TransportInputHandler"))
	}
}

func (h *TransportInputHandler) HandleEvent(eventPacket *core.EventPacket) error {
	h.SendPacket(eventPacket)
	return nil
}

// TransportOutputHandler handles outgoing data
type TransportOutputHandler struct {
	core.BaseHandler
	config  TransportConfig
	wrapper *TransportHandlerWrapper
}

func (h *TransportOutputHandler) Initialize(
	inputChan <-chan *core.EventPacket,
	outputNextChan chan<- *core.EventPacket,
	outputTopChan chan<- *core.EventPacket,
	ctx context.Context,
) error {
	err := h.BaseHandler.Initialize(inputChan, outputNextChan, outputTopChan, ctx)
	if err != nil {
		return err
	}
	h.BaseHandler.SetHandleEventFunc(h.HandleEvent)

	if !h.wrapper.connected {
		err = h.Service.(ITransportService).Connect()
		if err != nil {
			return err
		}
		h.wrapper.connected = true
	}
	return nil
}

func (h *TransportOutputHandler) Start() error {
	<-h.Ctx.Done()
	return nil
}

func (h *TransportOutputHandler) HandleEvent(eventPacket *core.EventPacket) error {
	err := h.Service.(ITransportService).SendEvent(eventPacket.Event)
	if err != nil {
		h.Logger.With(map[string]interface{}{"error": err}).Error("Failed to send event through transport service")
		return err
	}

	h.SendPacket(eventPacket)
	return nil
}

func (h *TransportOutputHandler) Cleanup() error {
	if h.wrapper.connected {
		h.Logger.Info("Cleaning up TransportOutputHandler and disconnecting service")
		h.wrapper.connected = false
	}
	h.Logger.Info("TransportOutputHandler cleaned up")
	return nil
}
