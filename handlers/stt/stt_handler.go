package stt

import (
	"context"
	"interactkit/core"
	"interactkit/events/stt"
	"interactkit/events/vad"
	"interactkit/utils/audio"
)

type ISTTService interface {
	core.IService
	StartTranscriptionSession(outChan chan<- string, interimOutputChan chan<- string, FatalServiceErrorChan chan<- error)
	SendTranscriptionAudio(audioData []byte) error
}

type STTHandler struct {
	core.BaseHandler
	messageOutChan chan string
	interimOutChan chan string
	config         STTConfig
}

func NewSTTHandler(service ISTTService, backupServices []ISTTService, ctx context.Context, config STTConfig) *STTHandler {
	typedServices := make([]core.IService, len(backupServices))
	for i, s := range backupServices {
		typedServices[i] = s
	}
	return &STTHandler{
		BaseHandler: *core.NewBaseHandler(
			service, typedServices, ctx,
		),
		config: config,
	}
}

func (h *STTHandler) Init(
	inputChan <-chan *core.EventPacket,
	outputNextChan chan<- *core.EventPacket,
	outputTopChan chan<- *core.EventPacket,
	ctx context.Context,
) error {
	h.messageOutChan = make(chan string)
	h.interimOutChan = make(chan string)
	return h.BaseHandler.Initialize(inputChan, outputNextChan, outputTopChan, ctx)
}

func (h *STTHandler) Start() error {
	// Create a goroutine to listen for incoming events and process them.
	go h.eventLoop()
	h.Service.(ISTTService).StartTranscriptionSession(h.messageOutChan, h.interimOutChan, h.FatalServiceErrorChan) // Start the transcription session when the handler starts.

	for {
		select {
		case input := <-h.InputChan:
			if err := h.HandleEvent(input); err != nil {
				h.FatalServiceErrorChan <- err
			}
		case <-h.Ctx.Done():
			// Handle context cancellation, which indicates that the handler should stop processing and clean up resources.
			return nil
		}
	}
}

func (h *STTHandler) eventLoop() {
	for {
		select {
		case eventPacket := <-h.messageOutChan:
			h.SendPacket(core.NewEventPacket(&stt.STTFinalOutputEvent{
				Text: eventPacket,
			}, core.EventRelayDestinationNextService, "STTHandler"))
		case interimResult := <-h.interimOutChan:
			h.SendPacket(
				core.NewEventPacket(&stt.STTInterimOutputEvent{
					Text: interimResult,
				}, core.EventRelayDestinationNextService, "STTHandler"),
			)

		case <-h.Ctx.Done():
			// Handle context cancellation, which indicates that the handler should stop processing and clean up resources.
			return
		}
	}
}

func (h *STTHandler) HandleEvent(eventPacket *core.EventPacket) error {
	switch event := eventPacket.Event.(type) {
	case *vad.VADUserSpeechChunkEvent:
		processedChunk, err := audio.ConvertAudioChunk(event.AudioChunk, h.config.RequiredAudioFormat, h.config.RequiredChannels, h.config.RequiredSampleRate)
		if err != nil {
			h.FatalServiceErrorChan <- err
			return nil
		}
		go h.Service.(ISTTService).SendTranscriptionAudio(*processedChunk.Data)
	default:
	}
	h.SendPacket(eventPacket)
	return nil
}

func (h *STTHandler) Cleanup() error {
	err := h.BaseHandler.Cleanup()
	close(h.interimOutChan)
	close(h.messageOutChan)
	return err
}
