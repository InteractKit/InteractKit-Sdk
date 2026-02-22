package activitycontrol

import (
	"context"
	"sync"
	"time"

	"interactkit/core"
	"interactkit/events/llm"
	"interactkit/events/tts"
	"interactkit/events/vad"
)

// Config holds configuration for ActivityControlHandler.
type Config struct {
	// ConfirmationTimeout is how long to wait for VadInterruptionConfirmedEvent
	// after a VadInterruptionSuspectedEvent before treating it as a false positive
	// and resuming audio playback. Default: 1500ms.
	ConfirmationTimeout time.Duration `json:"confirmation_timeout"`
	// RollbackDuration is how far back to "roll back" the effective speaking time
	// when an interruption is confirmed, to account for audio in flight. Default: 1500ms.
	RollbackDuration time.Duration `json:"rollback_duration"`
}

// DefaultConfig returns a Config with sensible defaults
func DefaultConfig() Config {
	return Config{
		ConfirmationTimeout: 1500 * time.Millisecond,
		RollbackDuration:    1500 * time.Millisecond,
	}
}

// ActivityControlHandler sits between the TTS handler and TransportOutput.
// It caches TTSOutputEvent audio chunks and gates their delivery to the transport:
//
//   - Normal: audio is forwarded immediately; sent duration is tracked.
//   - VadInterruptionSuspectedEvent: enter "suspended" mode — cache new audio instead
//     of forwarding it, and start a confirmation timer.
//   - VadInterruptionConfirmedEvent: drop the cache, emit TTSSpeakingEndedEvent to
//     broadcast (so VAD/TTS/etc. update bot-speaking state), then reset.
//   - Timer fires without confirmation (false positive): forward the cached audio and
//     resume normal operation.
type ActivityControlHandler struct {
	core.BaseHandler

	mu sync.Mutex

	// Audio cache — chunks buffered during "suspended" mode.
	cachedChunks []*core.EventPacket

	// Playback tracking.
	speakingStartTime time.Time
	totalSentDuration float64 // seconds of TTSOutputEvent audio forwarded to transport
	isSpeaking        bool

	// Interruption state.
	isSuspended  bool
	confirmTimer *time.Timer

	// resumeChan receives a signal when the confirmation timer fires (false positive path).
	resumeChan chan struct{}

	config Config
}

// dummyService is a no-op IService required by BaseHandler.
type dummyService struct{}

func (s *dummyService) Initialize(_ context.Context) error { return nil }
func (s *dummyService) Cleanup() error                     { return nil }
func (s *dummyService) Reset() error                       { return nil }

// NewActivityControlHandler creates a new ActivityControlHandler.
func NewActivityControlHandler(cfg Config, logger *core.Logger) *ActivityControlHandler {
	if cfg.ConfirmationTimeout == 0 {
		cfg.ConfirmationTimeout = 1500 * time.Millisecond
	}
	if cfg.RollbackDuration == 0 {
		cfg.RollbackDuration = 1500 * time.Millisecond
	}
	return &ActivityControlHandler{
		BaseHandler: *core.NewBaseHandler(&dummyService{}, nil, nil, logger),
		config:      cfg,
	}
}

func (h *ActivityControlHandler) Initialize(
	inputChan <-chan *core.EventPacket,
	outputNextChan chan<- *core.EventPacket,
	outputTopChan chan<- *core.EventPacket,
	ctx context.Context,
) error {
	h.resumeChan = make(chan struct{}, 1)
	return h.BaseHandler.Initialize(inputChan, outputNextChan, outputTopChan, ctx)
}

func (h *ActivityControlHandler) Start() error {
	go h.eventLoop()
	return nil
}

func (h *ActivityControlHandler) eventLoop() {
	for {
		select {
		case <-h.Ctx.Done():
			return
		case packet := <-h.InputChan:
			h.HandleEvent(packet)
		case <-h.resumeChan:
			h.onFalsePositive()
		}
	}
}

func (h *ActivityControlHandler) HandleEvent(packet *core.EventPacket) error {
	switch event := packet.Event.(type) {
	case *tts.TTSSpeakingStartedEvent:
		h.mu.Lock()
		h.speakingStartTime = time.Now()
		h.totalSentDuration = 0
		h.isSpeaking = true
		h.mu.Unlock()
		h.SendPacket(packet)

	case *tts.TTSSpeakingEndedEvent:
		h.mu.Lock()
		h.isSpeaking = false
		h.mu.Unlock()
		h.SendPacket(packet)

	case *llm.LLMResponseStartedEvent:
		// New response starting — drop any stale cached audio from the previous turn.
		h.mu.Lock()
		h.cachedChunks = nil
		h.speakingStartTime = time.Time{}
		h.totalSentDuration = 0
		h.mu.Unlock()
		h.SendPacket(packet)

	case *tts.TTSOutputEvent:
		h.mu.Lock()
		suspended := h.isSuspended
		h.mu.Unlock()

		if suspended {
			h.mu.Lock()
			h.cachedChunks = append(h.cachedChunks, packet)
			h.mu.Unlock()
		} else {
			dur := event.AudioChunk.GetDurationInSeconds()
			h.mu.Lock()
			h.totalSentDuration += dur
			h.mu.Unlock()
			h.SendPacket(packet)
		}

	case *vad.VadInterruptionSuspectedEvent:
		h.mu.Lock()
		if !h.isSuspended {
			h.isSuspended = true
			h.mu.Unlock()
			h.startConfirmTimer()
		} else {
			h.mu.Unlock()
		}
		h.SendPacket(packet)

	case *vad.VadInterruptionConfirmedEvent:
		h.stopConfirmTimer()

		h.mu.Lock()
		var approxPlayed float64
		if !h.speakingStartTime.IsZero() {
			approxPlayed = time.Since(h.speakingStartTime).Seconds()
			rollback := h.config.RollbackDuration.Seconds()
			approxPlayed -= rollback
			if approxPlayed < 0 {
				approxPlayed = 0
			}
		}
		totalSent := h.totalSentDuration
		wasSpeaking := h.isSpeaking

		// Drop cached audio — user interrupted, these chunks should not be played.
		h.cachedChunks = nil
		h.isSuspended = false
		h.isSpeaking = false
		h.totalSentDuration = 0
		h.speakingStartTime = time.Time{}
		h.mu.Unlock()

		unplayed := totalSent - approxPlayed
		if unplayed < 0 {
			unplayed = 0
		}
		h.Logger.With(map[string]any{
			"approx_played_s": approxPlayed,
			"total_sent_s":    totalSent,
			"unplayed_s":      unplayed,
		}).Info("ActivityControl: interruption confirmed, dropping cached audio")

		// Broadcast TTSSpeakingEndedEvent so all handlers (VAD, TTS state, etc.) know bot stopped.
		if wasSpeaking {
			h.SendPacket(core.NewEventPacket(
				&tts.TTSSpeakingEndedEvent{},
				core.EventRelayDestinationTopService,
				"ActivityControlHandler",
			))
		}

		h.SendPacket(packet)

	default:
		h.SendPacket(packet)
	}
	return nil
}

// startConfirmTimer arms the confirmation timer. When it fires without a
// VadInterruptionConfirmedEvent, onFalsePositive is called.
func (h *ActivityControlHandler) startConfirmTimer() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.confirmTimer = time.AfterFunc(h.config.ConfirmationTimeout, func() {
		select {
		case h.resumeChan <- struct{}{}:
		default:
		}
	})
}

// stopConfirmTimer cancels a pending confirmation timer (if any).
func (h *ActivityControlHandler) stopConfirmTimer() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.confirmTimer != nil {
		h.confirmTimer.Stop()
		h.confirmTimer = nil
	}
}

// onFalsePositive is called when the confirmation timer fires without a confirmed event.
// It resumes forwarding the cached audio chunks and exits suspended mode.
func (h *ActivityControlHandler) onFalsePositive() {
	h.mu.Lock()
	chunks := h.cachedChunks
	h.cachedChunks = nil
	h.isSuspended = false
	h.mu.Unlock()

	h.Logger.With(map[string]any{
		"cached_chunks": len(chunks),
	}).Info("ActivityControl: false positive interruption, resuming cached audio")

	for _, pkt := range chunks {
		if ev, ok := pkt.Event.(*tts.TTSOutputEvent); ok {
			dur := ev.AudioChunk.GetDurationInSeconds()
			h.mu.Lock()
			h.totalSentDuration += dur
			h.mu.Unlock()
		}
		h.SendPacket(pkt)
	}
}

func (h *ActivityControlHandler) Cleanup() error {
	h.stopConfirmTimer()
	return h.BaseHandler.Cleanup()
}

func (h *ActivityControlHandler) Reset() error {
	return h.BaseHandler.Reset()
}
