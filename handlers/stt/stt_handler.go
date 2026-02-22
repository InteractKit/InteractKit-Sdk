package stt

import (
	"context"
	"interactkit/core"
	"interactkit/events/stt"
	"interactkit/events/transport"
	"interactkit/events/vad"
)

type ISTTService interface {
	core.IService
	StartTranscriptionSession(outChan chan<- string, interimOutputChan chan<- string, FatalServiceErrorChan chan<- error)
	SendTranscriptionAudio(chunk core.AudioChunk) error
	Flush() error
}

type STTHandler struct {
	core.BaseHandler
	messageOutChan chan string
	interimOutChan chan string
	config         STTConfig
}

// NewSTTHandler creates a new STT handler.
// Use DefaultConfig() to get a config with sensible defaults and override only what you need.
// Chain WithBackupService to register fallback services.
func NewSTTHandler(service ISTTService, config STTConfig, logger *core.Logger) *STTHandler {
	return &STTHandler{
		BaseHandler: *core.NewBaseHandler(service, nil, nil, logger),
		config:      config,
	}
}

// WithBackupService registers a fallback service used when the primary fails.
// Returns the handler to allow chaining.
func (h *STTHandler) WithBackupService(service ISTTService) *STTHandler {
	h.BackupServices = append(h.BackupServices, service)
	return h
}

func (h *STTHandler) Cleanup() error {
	// Close STT channels to unblock eventLoop goroutine
	if h.messageOutChan != nil {
		close(h.messageOutChan)
		h.messageOutChan = nil
	}
	if h.interimOutChan != nil {
		close(h.interimOutChan)
		h.interimOutChan = nil
	}
	h.Logger.Info("STT Handler cleaned up")
	return h.BaseHandler.Cleanup()
}

func (h *STTHandler) Initialize(
	inputChan <-chan *core.EventPacket,
	outputNextChan chan<- *core.EventPacket,
	outputTopChan chan<- *core.EventPacket,
	ctx context.Context,
) error {
	h.messageOutChan = make(chan string)
	h.interimOutChan = make(chan string)
	err := h.BaseHandler.Initialize(inputChan, outputNextChan, outputTopChan, ctx)
	if err != nil {
		return err
	}
	h.BaseHandler.SetHandleEventFunc(h.HandleEvent)
	return nil
}

func (h *STTHandler) Start() error {
	// Create a goroutine to listen for incoming events and process them.
	go h.eventLoop()
	h.Service.(ISTTService).StartTranscriptionSession(h.messageOutChan, h.interimOutChan, h.FatalServiceErrorChan) // Start the transcription session when the handler starts.

	return nil
}

func (h *STTHandler) eventLoop() {
	for {
		select {
		case eventPacket, ok := <-h.messageOutChan:
			if !ok {
				// Channel closed, exit gracefully
				return
			}
			h.Logger.Debugf("STT Final Result: %s", eventPacket)
			h.SendPacket(core.NewEventPacket(&stt.STTFinalOutputEvent{
				Text: eventPacket,
			}, core.EventRelayDestinationNextService, "STTHandler"))
		case interimResult, ok := <-h.interimOutChan:
			if !ok {
				// Channel closed, exit gracefully
				return
			}
			h.Logger.Debugf("STT Interim Result: %s", interimResult)
			h.SendPacket(
				core.NewEventPacket(&stt.STTInterimOutputEvent{
					Text: interimResult,
				}, core.EventRelayDestinationNextService, "STTHandler"),
			)

		case <-h.Ctx.Done():
			h.Logger.With(map[string]interface{}{"handler": "STTHandler"}).Info("Context cancelled, stopping STTHandler event loop")
			// Handle context cancellation, which indicates that the handler should stop processing and clean up resources.
			return
		}
	}
}

func (h *STTHandler) HandleEvent(eventPacket *core.EventPacket) error {
	switch event := eventPacket.Event.(type) {

	case *transport.TransportAudioInputEvent:
		h.Service.(ISTTService).SendTranscriptionAudio(event.AudioChunk)
	case *vad.VadUserSpeechEndedEvent:
		err := h.Service.(ISTTService).Flush()
		if err != nil {
			h.FatalServiceErrorChan <- err
		}
	default:
	}
	h.SendPacket(eventPacket)
	return nil
}
