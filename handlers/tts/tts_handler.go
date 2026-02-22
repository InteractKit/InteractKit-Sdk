package tts

import (
	"context"
	"interactkit/core"
	"interactkit/events/llm"
	"interactkit/events/tts"
	"interactkit/events/vad"
	"strings"
	"sync"
	"time"
)

type TTSService interface {
	core.IService

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
	config            TTSConfig
	audioChunkOutChan chan core.AudioChunk
	errorChan         chan error
	doneChan          chan bool
	mu                sync.Mutex

	textBuffer string

	// Simulation fields - always available
	textToSpeak chan string // text chunks to be spoken
	resetSim    chan struct{}
	simCtx      context.Context
	simCancel   context.CancelFunc
	simWg       sync.WaitGroup

	// Speaking state tracking
	isSpeaking bool

	// Tracks whether we have sent at least one Speak payload that still needs a Flush.
	hasBufferedSinceLastFlush bool

	// Control simulation
	enableSimulation bool
}

func NewTTSHandler(service TTSService, config TTSConfig, logger *core.Logger) *TTSHandler {
	if len(config.BreakWords) == 0 {
		config.BreakWords = []string{
			// Punctuation
			",", ".", "?", "!", ";", ":", "—", "-", "(", ")", "[", "]", "{", "}", "\"", "'",

			// Coordinating conjunctions
			" and ", " but ", " or ", " nor ", " for ", " yet ", " so ",

			// Subordinating conjunctions
			" because ", " although ", " though ", " even though ",
			" since ", " unless ", " while ", " whereas ",
			" if ", " when ", " whenever ", " before ", " after ",
			" until ", " once ",

			// Relative/connector words
			" that ", " which ", " who ", " whom ", " whose ",

			// Transitional phrases
			" however ", " therefore ", " moreover ", " furthermore ",
			" nevertheless ", " consequently ", " thus ",
			" instead ", " otherwise ", " meanwhile ",

			// Common multi-word breaks
			" as well as ", " in order to ", " so that ",
			" as soon as ", " even if ",
		}
	}

	// Set default minimum length if not specified
	if config.MinTextLength == 0 {
		config.MinTextLength = 20 // Default minimum length
	}

	return &TTSHandler{
		BaseHandler:      *core.NewBaseHandler(service, nil, nil, logger),
		config:           config,
		enableSimulation: true, // Enable simulation by default
		isSpeaking:       false,
	}
}

// DisableSimulation turns off the simulation mode
func (h *TTSHandler) DisableSimulation() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.enableSimulation = false
}

// EnableSimulation turns on the simulation mode
func (h *TTSHandler) EnableSimulation() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.enableSimulation = true
}

func (h *TTSHandler) Initialize(
	inputChan <-chan *core.EventPacket,
	outputNextChan chan<- *core.EventPacket,
	outputTopChan chan<- *core.EventPacket,
	ctx context.Context,
) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Always create simulation channels
	h.textToSpeak = make(chan string, 100)
	h.resetSim = make(chan struct{}, 1)
	h.simCtx, h.simCancel = context.WithCancel(ctx)

	// Always create real TTS channels
	h.audioChunkOutChan = make(chan core.AudioChunk, 100)
	h.errorChan = make(chan error, 10)
	h.doneChan = make(chan bool, 1)

	err := h.BaseHandler.Initialize(inputChan, outputNextChan, outputTopChan, ctx)
	if err != nil {
		return err
	}
	h.BaseHandler.SetHandleEventFunc(h.HandleEvent)
	return nil
}

func (h *TTSHandler) HandleEvent(eventPacket *core.EventPacket) error {
	switch event := eventPacket.Event.(type) {
	case *llm.LLMResponseChunkEvent:
		if event.ConsumeImmediately {
			// Bypass buffering — speak this chunk right away (e.g. fillers).
			text := normalizeTextForTTS(event.Chunk)
			if text != "" {
				if err := h.Service.(TTSService).BufferText(text); err != nil {
					h.FatalServiceErrorChan <- err
				} else if err := h.Service.(TTSService).Flush(); err != nil {
					h.FatalServiceErrorChan <- err
				}
				if h.enableSimulation {
					select {
					case h.textToSpeak <- text:
					default:
						h.Logger.Info("TTS simulation: textToSpeak channel full, dropping immediate chunk")
					}
				}
			}
			break
		}

		h.mu.Lock()
		h.textBuffer += event.Chunk

		shouldFlush := false
		hasBreakWord := false

		// Check for break words
		for _, bw := range h.config.BreakWords {
			if idx := indexOfBreakWord(h.textBuffer, bw); idx != -1 {
				hasBreakWord = true
				break
			}
		}

		// Only flush if we have both a break word AND meet minimum length
		if hasBreakWord && len(h.textBuffer) >= h.config.MinTextLength {
			shouldFlush = true
		}

		if shouldFlush {
			text := normalizeTextForTTS(h.textBuffer)
			h.textBuffer = ""
			h.mu.Unlock()

			// Always buffer text for real TTS
			if err := h.Service.(TTSService).BufferText(text); err != nil {
				h.FatalServiceErrorChan <- err
			} else {
				h.mu.Lock()
				h.hasBufferedSinceLastFlush = true
				h.mu.Unlock()
			}

			// Also send to simulation if enabled
			if h.enableSimulation {
				select {
				case h.textToSpeak <- text:
				default:
					h.Logger.Info("TTS simulation: textToSpeak channel full, dropping chunk")
				}
			}
		} else {
			h.mu.Unlock()
		}

	case *llm.LLMResponseCompletedEvent:
		h.mu.Lock()
		remaining := strings.TrimSpace(h.textBuffer)
		h.textBuffer = ""
		hadBufferedText := h.hasBufferedSinceLastFlush
		h.mu.Unlock()

		if remaining != "" {
			if err := h.Service.(TTSService).BufferText(remaining); err != nil {
				h.FatalServiceErrorChan <- err
			} else {
				h.mu.Lock()
				h.hasBufferedSinceLastFlush = true
				h.mu.Unlock()
				hadBufferedText = true
			}

			// Send remaining text to simulation
			if h.enableSimulation {
				select {
				case h.textToSpeak <- remaining:
				default:
					h.Logger.Info("TTS simulation: textToSpeak channel full, dropping remaining chunk")
				}
			}
		}

		// Deepgram expects Flush only after at least one Speak payload.
		if hadBufferedText {
			if err := h.Service.(TTSService).Flush(); err != nil {
				h.FatalServiceErrorChan <- err
			} else {
				h.mu.Lock()
				h.hasBufferedSinceLastFlush = false
				h.mu.Unlock()
			}
		}

	case *vad.VadInterruptionSuspectedEvent:
		// Suspected interruption — pause simulation but don't reset TTS yet.
		// If confirmed, a VadInterruptionConfirmedEvent will follow and do the full reset.
		if h.enableSimulation {
			select {
			case h.resetSim <- struct{}{}:
			default:
			}
		}

	case *vad.VadInterruptionConfirmedEvent:
		h.mu.Lock()
		h.textBuffer = ""
		h.hasBufferedSinceLastFlush = false
		h.mu.Unlock()

		// Reset simulation if enabled
		if h.enableSimulation {
			select {
			case h.resetSim <- struct{}{}:
			default:
			}
		}

		// Reset real TTS
		if err := h.Service.(TTSService).Reset(); err != nil {
			h.FatalServiceErrorChan <- err
		}

	case *llm.LLMResponseStartedEvent:
		h.mu.Lock()
		h.textBuffer = ""
		h.hasBufferedSinceLastFlush = false
		h.mu.Unlock()

		// Reset simulation if enabled
		if h.enableSimulation {
			select {
			case h.resetSim <- struct{}{}:
			default:
			}
		}
		// Note: Real TTS may not need reset here

	case *tts.TTSSpeakEvent:
		// Immediately speak this text, bypassing chunk accumulation.
		if event.Text != "" {
			if err := h.Service.(TTSService).BufferText(event.Text); err != nil {
				h.FatalServiceErrorChan <- err
			} else if err := h.Service.(TTSService).Flush(); err != nil {
				h.FatalServiceErrorChan <- err
			}
			if h.enableSimulation {
				select {
				case h.textToSpeak <- event.Text:
				default:
					h.Logger.Info("TTS simulation: textToSpeak channel full, dropping TTSSpeakEvent text")
				}
			}
		}

	default:
		// pass through
	}

	h.SendPacket(eventPacket)
	return nil
}

// simulationLoop processes text chunks word by word at ~150 wpm.
func (h *TTSHandler) simulationLoop() {
	defer h.simWg.Done()

	var words []string
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
		// Ensure we send ended event if we're still speaking when loop exits
		h.mu.Lock()
		if h.isSpeaking {
			h.isSpeaking = false
			h.mu.Unlock()
			h.SendPacket(core.NewEventPacket(
				&tts.TTSSpeakingEndedEvent{},
				core.EventRelayDestinationTopService,
				"TTSHandler",
			))
		} else {
			h.mu.Unlock()
		}
	}()

	for {
		// Prepare timer channel (nil if no timer is active)
		var timerC <-chan time.Time
		if timer != nil {
			timerC = timer.C
		}

		select {
		case <-h.simCtx.Done():
			return

		case text := <-h.textToSpeak:
			newWords := strings.Fields(text)
			if len(newWords) == 0 {
				continue
			}

			// Check if we were not speaking before adding these words
			h.mu.Lock()
			wasSpeaking := h.isSpeaking
			if !wasSpeaking && len(words) == 0 {
				h.isSpeaking = true
				h.mu.Unlock()
				// Send speaking started event
				h.SendPacket(core.NewEventPacket(
					&tts.TTSSpeakingStartedEvent{},
					core.EventRelayDestinationTopService,
					"TTSHandler",
				))
			} else {
				h.mu.Unlock()
			}

			words = append(words, newWords...)
			// If no timer is running, start processing immediately.
			if timer == nil && len(words) > 0 {
				h.sendNextWord(&words, &timer)
			}

		case <-h.resetSim:
			// Clear queue and drain any text already buffered in the channel.
			words = nil
		drainLoop:
			for {
				select {
				case <-h.textToSpeak:
				default:
					break drainLoop
				}
			}
			if timer != nil {
				timer.Stop()
				timer = nil
			}

			// Send speaking ended event if we were speaking
			h.mu.Lock()
			if h.isSpeaking {
				h.isSpeaking = false
				h.mu.Unlock()
				h.SendPacket(core.NewEventPacket(
					&tts.TTSSpeakingEndedEvent{},
					core.EventRelayDestinationTopService,
					"TTSHandler",
				))
			} else {
				h.mu.Unlock()
			}

		case <-timerC:
			// Time to speak the next word.
			if len(words) > 0 {
				h.sendNextWord(&words, &timer)
			} else {
				// Should not happen, but safety.
				timer.Stop()
				timer = nil

				// No more words, so speaking has ended
				h.mu.Lock()
				if h.isSpeaking {
					h.isSpeaking = false
					h.mu.Unlock()
					h.SendPacket(core.NewEventPacket(
						&tts.TTSSpeakingEndedEvent{},
						core.EventRelayDestinationTopService,
						"TTSHandler",
					))
				} else {
					h.mu.Unlock()
				}
			}
		}
	}
}

// sendNextWord removes the first word, sends a TTSAudioSpokenEvent,
// and (re)starts the timer for the following word if any remain.
func (h *TTSHandler) sendNextWord(words *[]string, timer **time.Timer) {
	wpm := 130
	interval := time.Duration(60000/wpm) * time.Millisecond
	if len(*words) == 0 {
		return
	}
	word := (*words)[0]
	*words = (*words)[1:]

	// Emit the spoken word event.
	h.SendPacket(core.NewEventPacket(
		&tts.TTSSpokenTextChunkEvent{Text: word},
		core.EventRelayDestinationNextService,
		"TTSHandler",
	))

	// Schedule the next word if there is one.
	if len(*words) > 0 {
		if *timer == nil {
			*timer = time.NewTimer(interval)
		} else {
			// Reset the existing timer.
			if !(*timer).Stop() {
				// Drain the channel if necessary.
				select {
				case <-(*timer).C:
				default:
				}
			}
			(*timer).Reset(interval)
		}
	} else {
		// No more words, stop the timer and send speaking ended event
		if *timer != nil {
			(*timer).Stop()
			*timer = nil
		}

		// Speaking has ended
		h.mu.Lock()
		if h.isSpeaking {
			h.isSpeaking = false
			h.mu.Unlock()
			h.SendPacket(core.NewEventPacket(
				&tts.TTSSpeakingEndedEvent{},
				core.EventRelayDestinationTopService,
				"TTSHandler",
			))
		} else {
			h.mu.Unlock()
		}
	}
}

func (h *TTSHandler) Start() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Always start simulation if enabled
	if h.enableSimulation {
		h.simWg.Add(1)
		go h.simulationLoop()
		h.Logger.Info("TTS simulation started")
	}

	// Always start real TTS session
	err := h.Service.(TTSService).StartTTSSession(h.audioChunkOutChan, h.errorChan, h.doneChan)
	if err != nil {
		h.Logger.Errorf("Error starting TTS session: %v", err)
		h.FatalServiceErrorChan <- err
		return err
	}
	h.Logger.Info("Real TTS session started")

	// Always listen for audio and errors
	go h.listenForAudioAndErrors()

	return nil
}

func (h *TTSHandler) listenForAudioAndErrors() {
	for {
		select {
		case audioChunk := <-h.audioChunkOutChan:
			h.SendPacket(core.NewEventPacket(
				&tts.TTSOutputEvent{AudioChunk: audioChunk},
				core.EventRelayDestinationNextService,
				"TTSHandler",
			))
		case err := <-h.errorChan:
			h.FatalServiceErrorChan <- err
		case <-h.doneChan:
			h.Logger.Info("TTS session done")
			return
		case <-h.Ctx.Done():
			return
		}
	}
}

func (h *TTSHandler) Cleanup() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Always cleanup simulation if it was started
	if h.simCancel != nil {
		h.simCancel()
	}
	h.simWg.Wait()

	if h.textToSpeak != nil {
		close(h.textToSpeak)
		h.textToSpeak = nil
	}
	if h.resetSim != nil {
		close(h.resetSim)
		h.resetSim = nil
	}
	h.Logger.Info("TTS simulation cleaned up")

	// Always cleanup real TTS channels
	if h.audioChunkOutChan != nil {
		close(h.audioChunkOutChan)
		h.audioChunkOutChan = nil
	}
	if h.errorChan != nil {
		close(h.errorChan)
		h.errorChan = nil
	}
	if h.doneChan != nil {
		close(h.doneChan)
		h.doneChan = nil
	}
	h.Logger.Info("Real TTS channels cleaned up")

	h.Logger.Info("TTS Handler cleaned up")
	return h.BaseHandler.Cleanup()
}

// Helper functions remain unchanged.
func indexOfBreakWord(text, breakWord string) int {
	return strings.Index(text, breakWord)
}
