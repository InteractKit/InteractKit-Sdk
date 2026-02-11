package vad

import (
	"interactkit/core"
	"interactkit/events/transport"
	"interactkit/events/vad"
)

type VADService interface {
	ProcessAudio(input core.AudioChunk) (core.VADResult, error)
}

type VADHandler struct {
	core.BaseHandler
	Service VADService
	config  VADConfig
}

func NewVADHandler(service VADService, config VADConfig) *VADHandler {
	return &VADHandler{
		Service: service,
		config:  config,
	}
}

func (h *VADHandler) HandleEvent(eventPacket *core.EventPacket) error {
	switch event := eventPacket.Event.(type) {
	case *transport.TransportAudioInputEvent:
		go func() {
			vadResult, err := h.Service.ProcessAudio(event.AudioChunk)
			if err != nil {
				h.FatalServiceErrorChan <- err
				return
			}
			if vadResult.IsSpeech && vadResult.Confidence >= h.config.MinConfidence {
				h.SendPacket(core.NewEventPacket(&vad.VADUserSpeechChunkEvent{
					AudioChunk: event.AudioChunk,
				}, core.EventRelayDestinationNextService, "VADHandler"))
			} else {
				h.SendPacket(core.NewEventPacket(&vad.VADSilenceChunkEvent{
					AudioChunk: event.AudioChunk,
				}, core.EventRelayDestinationNextService, "VADHandler"))
			}
		}()
	default:
	}
	h.SendPacket(eventPacket)
	return nil
}
