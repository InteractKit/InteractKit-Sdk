package vad

import (
	"context"
	"interactkit/core"
	"interactkit/events/transport"
	"interactkit/events/tts"
	"interactkit/events/vad"
	"time"
)

type VADService interface {
	core.IService
	ProcessAudio(input core.AudioChunk) (core.VADResult, error)
}

type VADHandler struct {
	core.BaseHandler
	config          VADConfig
	lastSpeechTime  time.Time
	isInGracePeriod bool
	isSpeechActive  bool

	// New fields for interruption handling
	botSpeaking           bool          // whether the bot is currently speaking
	baseGracePeriod       time.Duration // normal grace period (150ms)
	currentGracePeriod    time.Duration // effective grace period (may be increased)
	lastInterruptionTime  time.Time     // last time an interruption occurred
	patienceResetDuration time.Duration // time after which increased patience reverts
}

func NewVADHandler(service VADService, config VADConfig, logger *core.Logger) *VADHandler {
	baseGrace := 300 * time.Millisecond
	return &VADHandler{
		BaseHandler:           *core.NewBaseHandler(service, nil, nil, logger),
		config:                config,
		lastSpeechTime:        time.Time{},
		isInGracePeriod:       false,
		isSpeechActive:        false,
		botSpeaking:           false,
		baseGracePeriod:       baseGrace,
		currentGracePeriod:    baseGrace,
		patienceResetDuration: 5 * time.Second, // could be made configurable
	}
}

func (h *VADHandler) Initialize(
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
	return nil
}

func (h *VADHandler) Start() error {
	return nil
}

func (h *VADHandler) HandleEvent(eventPacket *core.EventPacket) error {
	switch event := eventPacket.Event.(type) {
	case *transport.TransportAudioInputEvent:
		h.handleTransportAudio(event)
	case *tts.TTSSpeakingStartedEvent: // assume such an event exists
		h.Logger.Info("Bot started speaking")
		h.botSpeaking = true
	case *tts.TTSSpeakingEndedEvent: // assume such an event exists
		h.Logger.Info("Bot ended speaking")
		h.botSpeaking = false
		// maybe also handle TTSSpokenTextChunkEvent if needed
	}
	// Always forward the packet downstream
	h.SendPacket(eventPacket)
	return nil
}

func (h *VADHandler) handleTransportAudio(event *transport.TransportAudioInputEvent) {
	currentTime := time.Now()

	// Reset patience if enough time has passed since the last interruption
	if !h.lastInterruptionTime.IsZero() && currentTime.Sub(h.lastInterruptionTime) > h.patienceResetDuration {
		h.currentGracePeriod = h.baseGracePeriod
		h.lastInterruptionTime = time.Time{}
	}

	// Process the audio chunk through VAD service
	vadResult, err := h.Service.(VADService).ProcessAudio(event.AudioChunk)
	if err != nil {
		h.FatalServiceErrorChan <- err
		return
	}

	// Check if this chunk contains speech with sufficient confidence
	isSpeech := vadResult.Confidence >= h.config.MinConfidence

	if isSpeech {
		// Speech detected - update last speech time
		h.lastSpeechTime = currentTime
		h.isInGracePeriod = false

		// If speech was not previously active, it's a start of user speech
		if !h.isSpeechActive {
			h.isSpeechActive = true

			// Send speech started event
			h.SendPacket(core.NewEventPacket(&vad.VadUserSpeechStartedEvent{}, core.EventRelayDestinationNextService, "VADHandler"))

			// If bot is speaking, this is an interruption
			if h.botSpeaking && h.config.AllowInterruptions {

				// Send interruption event
				h.SendPacket(core.NewEventPacket(&vad.VadInterruptionSuspectedEvent{}, core.EventRelayDestinationNextService, "VADHandler"))

				// Increase the grace period
				increase := time.Duration(h.config.VadPatienceIncreaseOnInterruption * float32(time.Second))
				h.currentGracePeriod = h.baseGracePeriod + increase
				h.lastInterruptionTime = currentTime

				h.Logger.With(map[string]any{
					"new_grace_period": h.currentGracePeriod,
				}).Info("User interrupted bot, VAD patience increased")
			}
		}

		h.SendPacket(core.NewEventPacket(&vad.VADUserSpeakingEvent{}, core.EventRelayDestinationNextService, "VADHandler"))
	} else {
		// No speech detected in this chunk
		timeSinceLastSpeech := currentTime.Sub(h.lastSpeechTime)

		// Check if we're within the current grace period
		if !h.lastSpeechTime.IsZero() && timeSinceLastSpeech <= h.currentGracePeriod {
			// We're in the grace period - treat as speech
			if !h.isInGracePeriod {
				h.isInGracePeriod = true
			}

			// If speech was not previously active, send started event
			if !h.isSpeechActive {
				h.isSpeechActive = true
				h.SendPacket(core.NewEventPacket(&vad.VadUserSpeechStartedEvent{}, core.EventRelayDestinationNextService, "VADHandler"))
			}

			h.SendPacket(core.NewEventPacket(&vad.VADUserSpeakingEvent{}, core.EventRelayDestinationNextService, "VADHandler"))
		} else {
			// Outside grace period - treat as silence
			h.isInGracePeriod = false

			// If speech was previously active, send ended event
			if h.isSpeechActive {
				h.isSpeechActive = false
				h.Logger.Info("VAD detected end of speech after grace period")
				h.SendPacket(core.NewEventPacket(&vad.VadUserSpeechEndedEvent{}, core.EventRelayDestinationNextService, "VADHandler"))
			}

			h.SendPacket(core.NewEventPacket(&vad.VADSilenceEvent{}, core.EventRelayDestinationNextService, "VADHandler"))
		}
	}
}
