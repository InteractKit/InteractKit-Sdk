package tts

import (
	"context"
	"interactkit/core"
	"interactkit/events/llm"
	"interactkit/events/tts"
	"interactkit/events/vad"
)

type TTSService interface {
	StartTTSSession(
		outChan chan<- core.AudioChunk,
		errorChan chan<- error,
		doneChan chan<- bool,
	) error
	BufferText(text string) error
	Flush() error
	Reset() error
}

type TTSHandler struct {
	core.BaseHandler
	Service           TTSService
	config            TTSConfig
	audioChunkOutChan chan core.AudioChunk
	errorChan         chan error
	doneChan          chan bool
}

func NewTTSHandler(service TTSService, config TTSConfig) *TTSHandler {
	return &TTSHandler{
		Service: service,
		config:  config,
	}
}

func (h *TTSHandler) Init(
	inputChan <-chan *core.EventPacket,
	outputNextChan chan<- *core.EventPacket,
	outputTopChan chan<- *core.EventPacket,
	ctx context.Context,
) error {
	h.audioChunkOutChan = make(chan core.AudioChunk)
	h.errorChan = make(chan error)
	h.doneChan = make(chan bool)
	return h.BaseHandler.Initialize(inputChan, outputNextChan, outputTopChan, ctx)
}

func (h *TTSHandler) HandleEvent(eventPacket *core.EventPacket) error {
	switch event := eventPacket.Event.(type) {
	case *llm.LLMResponseChunkEvent:
		go func() {
			if err := h.Service.BufferText(event.Chunk); err != nil {
				h.FatalServiceErrorChan <- err
			}
		}() // When a new chunk of LLM response text is received, we buffer it in the TTS service. This allows the TTS engine to start generating audio output for the received text chunk while waiting for additional chunks to arrive, enabling a more responsive user experience.
	case *llm.LLMResponseCompletedEvent:
		go func() {
			if err := h.Service.Flush(); err != nil {
				h.FatalServiceErrorChan <- err
			}
		}() // When the LLM response is completed, we flush the TTS buffer to ensure that all buffered text is sent to the TTS engine for synthesis. This allows the system to start generating audio output as soon as the complete text is available, rather than waiting for additional events or input.
	case *vad.VadInterruptionDetectedEvent:
		go func() {
			if err := h.Service.Reset(); err != nil {
				h.FatalServiceErrorChan <- err
			}
		}() // If a VAD interruption is detected, we reset the TTS session to stop any ongoing speech and clear the buffer. This allows the system to respond more quickly to user interruptions, such as when the user starts speaking again while the TTS is still outputting audio.
	default:
	}
	h.SendPacket(eventPacket)
	return nil
}

func (h *TTSHandler) Start() error {
	// Start the TTS session in a separate goroutine. This will allow the handler to listen for incoming events and process them while the TTS session is active.
	go func() {
		err := h.Service.StartTTSSession(h.audioChunkOutChan, h.errorChan, h.doneChan)
		if err != nil {
			h.FatalServiceErrorChan <- err
			return
		}
	}()

	for {
		select {
		case audioChunk := <-h.audioChunkOutChan:
			h.SendPacket(core.NewEventPacket(&tts.TTSOutputEvent{
				AudioChunk: audioChunk,
			}, core.EventRelayDestinationNextService, "TTSHandler"))
		case err := <-h.errorChan:
			h.FatalServiceErrorChan <- err
		case <-h.doneChan:
			return nil
		case <-h.Ctx.Done():
			// Handle context cancellation, which indicates that the handler should stop processing and clean up resources.
			return nil
		}
	}
}

func (h *TTSHandler) Cleanup() error {
	err := h.BaseHandler.Cleanup()
	// Additional cleanup logic can be added here if needed, such as closing channels or stopping the TTS session.
	return err
}
