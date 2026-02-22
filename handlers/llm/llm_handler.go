package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"interactkit/core"
	"interactkit/events/llm"
	"interactkit/events/tts"
	"interactkit/events/vad"
)

type LLMService interface {
	core.IService
	RunCompletion(
		context core.LLMContext,
		outChan chan<- string,
		toolInvocationChan chan<- core.LLMToolCall,
		FatalServiceErrorChan chan<- error,
		completionStartChan chan<- struct{},
		completionEndChan chan<- struct{},
	)
	GenerateJsonOutput(
		context core.LLMContext,
	) (map[string]any, error)
}

type LLMHandler struct {
	core.BaseHandler
	messageOutChan        chan string
	toolInvocationOutChan chan core.LLMToolCall
	completionStartChan   chan struct{}
	completionEndChan     chan struct{}
	config                LLMHandlerConfig
	filler                *fillerState
	fillerService         LLMService // optional lighter service used only for filler generation
	discarding            int32      // atomic bool: 1 = discard stale chunks from cancelled generation
}

// NewLLMHandler creates a new LLM handler.
// Use DefaultConfig() to get a config with sensible defaults and override only what you need.
// Chain WithBackupService and WithFillerService to register optional services.
func NewLLMHandler(service LLMService, config LLMHandlerConfig, logger *core.Logger) *LLMHandler {
	return &LLMHandler{
		BaseHandler: *core.NewBaseHandler(service, nil, nil, logger),
		config:      config,
		filler:      newFillerState(config.GenerateFillers),
	}
}

// WithBackupService registers a fallback service used when the primary fails.
// Returns the handler to allow chaining.
func (h *LLMHandler) WithBackupService(service LLMService) *LLMHandler {
	h.BackupServices = append(h.BackupServices, service)
	return h
}

// WithFillerService sets a dedicated lighter service used only for filler generation.
// Returns the handler to allow chaining.
func (h *LLMHandler) WithFillerService(service LLMService) *LLMHandler {
	h.fillerService = service
	return h
}

func (h *LLMHandler) Initialize(
	inputChan <-chan *core.EventPacket,
	outputNextChan chan<- *core.EventPacket,
	outputTopChan chan<- *core.EventPacket,
	ctx context.Context,
) error {
	h.messageOutChan = make(chan string, 10)
	h.toolInvocationOutChan = make(chan core.LLMToolCall)
	h.completionStartChan = make(chan struct{}, 1)
	h.completionEndChan = make(chan struct{}, 1)
	err := h.BaseHandler.Initialize(inputChan, outputNextChan, outputTopChan, ctx)
	if h.fillerService != nil {
		h.fillerService.(core.IService).Initialize(ctx)
	}
	if err != nil {
		return err
	}
	h.BaseHandler.SetHandleEventFunc(h.HandleEvent)
	return nil
}

func (h *LLMHandler) Start() error {
	// Create a goroutine to listen for incoming events and process them.
	go h.eventLoop()
	return nil
}

func (h *LLMHandler) eventLoop() {
	var fullText string
	// listen messages on unique channels for LLM messages, tool invocations, and fatal errors. This allows the handler to process these events and relay them to the appropriate destinations.
	for {
		select {
		case msg := <-h.messageOutChan:
			if atomic.LoadInt32(&h.discarding) == 0 {
				// Relay the LLM message to the next service in the pipeline.
				h.SendPacket(core.NewEventPacket(&llm.LLMResponseChunkEvent{
					Chunk: msg,
				}, core.EventRelayDestinationNextService, "LLMHandler"))
				fullText += msg
			}
		case toolCall := <-h.toolInvocationOutChan:
			if atomic.LoadInt32(&h.discarding) == 0 {
				// Relay the tool invocation request to the next service in the pipeline.
				h.SendPacket(core.NewEventPacket(&llm.LLMToolInvocationRequestedEvent{
					ToolId: toolCall.ToolId,
					Params: toolCall.Parameters,
				}, core.EventRelayDestinationNextService, "LLMHandler"))
			}
		case <-h.completionStartChan:
			if atomic.LoadInt32(&h.discarding) == 0 {
				// Reset aggregation and include any consumed filler
				if filler := h.getConsumedFiller(); filler != "" {
					fullText = filler
				} else {
					fullText = ""
				}
			}
		case <-h.completionEndChan:
			if atomic.LoadInt32(&h.discarding) == 0 {
				h.SendPacket(
					core.NewEventPacket(
						&llm.LLMResponseCompletedEvent{
							FullText: fullText,
						},
						core.EventRelayDestinationNextService,
						"LLMHandler",
					),
				)
				h.clearFillerAfterResponse()
			}
			// Reset fullText for the next generation regardless
			fullText = ""
		case <-h.Ctx.Done():
			return
		}
	}
}

func (h *LLMHandler) HandleEvent(packet *core.EventPacket) error {
	switch e := packet.Event.(type) {
	case *llm.LLMVariableConfirmRequestEvent:
		// Consume the filler that was already spoken so the bridge can incorporate it.
		// Clear filler state so it doesn't bleed into any subsequent LLM generation.
		consumedFiller := h.filler.getConsumedFiller()
		h.filler.clearAfterResponse()
		go h.generateConfirmBridge(consumedFiller, e.ConfirmText)
		return nil // consumed — do not relay downstream

	case *llm.LLMGenerateResponseEvent:
		// Accept chunks for the new generation
		atomic.StoreInt32(&h.discarding, 0)

		// Send response started event immediately
		h.SendPacket(
			core.NewEventPacket(
				&llm.LLMResponseStartedEvent{},
				core.EventRelayDestinationNextService,
				"LLMHandler",
			),
		)

		// Start LLM completion asynchronously so the handler remains unblocked
		// and can receive interruption events during generation.
		// The goroutine waits briefly for the filler to arrive (it is generated
		// concurrently and may not be ready yet), then prepends it before
		// starting the main completion.
		go func(e *llm.LLMGenerateResponseEvent) {
			// Wait up to 300 ms for an in-flight filler generation to finish.
			// If no filler was requested (short utterance), this returns immediately.
			// NOTE: We must wait BEFORE resetting the service because when no
			// dedicated fillerService is configured, the filler goroutine uses
			// the main service's context. Resetting first would cancel that
			// context and kill the in-flight filler API call.
			filler := h.waitForFiller(300 * time.Millisecond)

			h.Service.Reset() // Ensure any in-flight generation is cancelled when starting a new one.
			completionCtx := e.Context
			if filler != "" {
				h.Logger.Infof("Prepending filler to response: '%s'", filler)
				h.SendPacket(
					core.NewEventPacket(
						&llm.LLMResponseChunkEvent{Chunk: filler, ConsumeImmediately: true},
						core.EventRelayDestinationNextService,
						"LLMHandler",
					),
				)
				// Tell the LLM it already started speaking with the filler so it
				// continues coherently (e.g. "Hmm, let me think..." → continues the thought).
				msgs := make([]core.LLMMessage, len(e.Context.Messages))
				copy(msgs, e.Context.Messages)
				completionCtx = core.LLMContext{
					Messages: append(msgs, core.LLMMessage{
						Role: core.LLMMessageRoleSystem,
						Message: fmt.Sprintf(
							"The response already starts with \"%s\". Do NOT repeat it. Continue seamlessly from that point. Begin your output with \"...\" followed by the continuation.",
							filler,
						),
					}),
					Tools: e.Context.Tools,
				}
			}

			h.Service.(LLMService).RunCompletion(
				completionCtx,
				h.messageOutChan,
				h.toolInvocationOutChan,
				h.FatalServiceErrorChan,
				h.completionStartChan,
				h.completionEndChan,
			)
		}(e)

	case *vad.VadInterruptionSuspectedEvent:
		// Stop forwarding in-flight chunks — wait for confirmation before resetting stream.
		atomic.StoreInt32(&h.discarding, 1)
		h.filler.clearAfterResponse()

	case *vad.VadInterruptionConfirmedEvent:
		// Interruption confirmed — cancel the in-flight stream.
		atomic.StoreInt32(&h.discarding, 1)
		h.Service.Reset()
		h.filler.clearAfterResponse()

	case *llm.LLMPrepareInterimFillerEvent:
		h.handleInterimTranscript(e.PartialTranscript, e.Context)
	default:
	}
	h.SendPacket(packet)
	return nil
}

func (h *LLMHandler) Cleanup() error {
	if h.fillerService != nil {
		err := h.fillerService.(core.IService).Cleanup()
		if err != nil {
			return err
		}
	}
	return h.BaseHandler.Cleanup()
}

func (h *LLMHandler) handleInterimTranscript(text string, ctx *core.LLMContext) {
	if h.filler == nil {
		return
	}
	shouldRequest, requestText, utteranceID, token := h.filler.onInterim(text)
	if !shouldRequest {
		return
	}
	go h.generateFiller(requestText, *ctx, utteranceID, token)
}

func (h *LLMHandler) consumePendingFiller() string {
	if h.filler == nil {
		return ""
	}
	return h.filler.consumeFiller()
}

func (h *LLMHandler) getConsumedFiller() string {
	if h.filler == nil {
		return ""
	}
	return h.filler.getConsumedFiller()
}

func (h *LLMHandler) clearFillerAfterResponse() {
	if h.filler == nil {
		return
	}
	h.filler.clearAfterResponse()
}

func (h *LLMHandler) waitForFiller(timeout time.Duration) string {
	if h.filler == nil {
		return ""
	}
	return h.filler.waitForFiller(timeout)
}

func (h *LLMHandler) generateFiller(partial string, llmCtx core.LLMContext, utteranceID, token uint64) {
	// Prefer the dedicated filler service (lighter model); fall back to the main service.
	var service LLMService
	if h.fillerService != nil {
		service = h.fillerService
	} else {
		var ok bool
		service, ok = h.Service.(LLMService)
		if !ok {
			return
		}
	}
	lastFewContextWords := ""
	// find last assistant message in context and take last 5 words to include in filler prompt for better contextual awareness
	for i := len(llmCtx.Messages) - 1; i >= 0; i-- {
		msg := llmCtx.Messages[i]
		if msg.Role == core.LLMMessageRoleAssistant {
			lastFewContextWords = msg.Message
			break
		}
	}
	ctx := core.LLMContext{
		Messages: []core.LLMMessage{
			{Role: core.LLMMessageRoleSystem, Message: FILLER_PROMPT},
			{Role: core.LLMMessageRoleSystem, Message: "Context for filler generation: " + lastFewContextWords},
			{Role: core.LLMMessageRoleUser, Message: "Generate a filler for this partial transcript:\n " + partial + "..."},
		},
	}
	result, err := service.GenerateJsonOutput(ctx)
	if err != nil {
		h.Logger.Warnf("failed to generate filler: %v", err)
		h.filler.requestFailed(token, utteranceID)
		return
	}
	filler, skip, err := parseFillerResponse(result)
	if err != nil {
		h.Logger.Warnf("failed to decode filler response: %v", err)
		h.filler.requestFailed(token, utteranceID)
		return
	}
	h.Logger.Infof("Decoded filler response: filler='%s', skip=%v", filler, skip)
	h.filler.storeResult(token, utteranceID, filler, skip)
}

func parseFillerResponse(raw map[string]any) (string, bool, error) {
	data, err := json.Marshal(raw)
	if err != nil {
		return "", true, err
	}
	var payload struct {
		Filler     string `json:"filler"`
		Skip       bool   `json:"skip"`
		Prediction string `json:"prediction"` // for backward compatibility with older prompt templates that return { "prediction": "the filler" }
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", true, err
	}
	return strings.TrimSpace(payload.Filler), payload.Skip || strings.TrimSpace(payload.Filler) == "", nil
}

// generateConfirmBridge fires a TTSSpeakEvent with a phrase that naturally
// bridges the already-spoken filler into the confirmation question. If no
// filler was consumed, the confirmText is delivered as-is. On AI failure the
// confirmText is used as a direct fallback.
func (h *LLMHandler) generateConfirmBridge(consumedFiller, confirmText string) {
	sendConfirm := func(text string) {
		h.SendPacket(core.NewEventPacket(
			&tts.TTSSpeakEvent{Text: text},
			core.EventRelayDestinationNextService,
			"LLMHandler",
		))
	}

	// No filler was spoken — nothing to bridge, deliver confirm directly.
	if consumedFiller == "" {
		sendConfirm(confirmText)
		return
	}

	var service LLMService
	if h.fillerService != nil {
		service = h.fillerService
	} else if svc, ok := h.Service.(LLMService); ok {
		service = svc
	}
	if service == nil {
		sendConfirm(confirmText)
		return
	}

	ctx := core.LLMContext{
		Messages: []core.LLMMessage{
			{Role: core.LLMMessageRoleSystem, Message: VARIABLE_CONFIRM_BRIDGE_PROMPT},
			{Role: core.LLMMessageRoleUser, Message: fmt.Sprintf(
				"Already spoken: %q\nMust deliver: %q",
				consumedFiller, confirmText,
			)},
		},
	}
	result, err := service.GenerateJsonOutput(ctx)
	if err != nil {
		h.Logger.Infof("LLMHandler: confirm bridge generation failed: %v — using confirm text directly", err)
		sendConfirm(confirmText)
		return
	}
	text := parseConfirmBridgeResponse(result, confirmText)
	h.Logger.Infof("LLMHandler: confirm bridge: filler=%q → bridge=%q", consumedFiller, text)
	sendConfirm(text)
}

func parseConfirmBridgeResponse(raw map[string]any, fallback string) string {
	if t, ok := raw["text"].(string); ok && strings.TrimSpace(t) != "" {
		return strings.TrimSpace(t)
	}
	return fallback
}

type fillerState struct {
	enabled            bool
	mu                 sync.Mutex
	listening          bool
	currentUtteranceID uint64
	fillerUtteranceID  uint64
	fillerRequested    bool
	fillerReady        bool
	fillerConsumed     bool
	pendingFiller      string
	consumedFiller     string
	activeRequestToken uint64
	nextRequestToken   uint64
}

func newFillerState(enabled bool) *fillerState {
	return &fillerState{enabled: enabled}
}

func (f *fillerState) onInterim(text string) (bool, string, uint64, uint64) {
	if f == nil || !f.enabled {
		return false, "", 0, 0
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false, "", 0, 0
	}
	words := len(strings.Fields(trimmed))
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.listening {
		f.listening = true
		f.currentUtteranceID++
		f.resetUtteranceLocked()
	}
	// Re-generate filler on every interim with enough words to keep the candidate fresh.
	// Skip only once the filler has been consumed (response is already underway).
	if words <= 3 || f.fillerConsumed {
		return false, "", 0, 0
	}
	// Invalidate any previous in-flight or pending result so waitForFiller
	// waits for the fresh generation triggered by this interim.
	f.fillerReady = false
	f.pendingFiller = ""
	f.fillerRequested = true
	f.fillerUtteranceID = f.currentUtteranceID
	f.nextRequestToken++
	token := f.nextRequestToken
	f.activeRequestToken = token
	return true, trimmed, f.currentUtteranceID, token
}

func (f *fillerState) resetUtteranceLocked() {
	f.fillerRequested = false
	f.fillerReady = false
	f.fillerConsumed = false
	f.pendingFiller = ""
	f.consumedFiller = ""
	f.fillerUtteranceID = 0
	f.activeRequestToken = 0
}

func (f *fillerState) consumeFiller() string {
	if f == nil || !f.enabled {
		return ""
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// Consume filler if ready and not already consumed
	if f.fillerConsumed || !f.fillerReady {
		return ""
	}
	filler := f.pendingFiller
	if filler == "" {
		return ""
	}
	f.fillerConsumed = true
	f.consumedFiller = filler // Store for fullText tracking
	return filler
}

func (f *fillerState) getConsumedFiller() string {
	if f == nil || !f.enabled {
		return ""
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.consumedFiller
}

func (f *fillerState) clearAfterResponse() {
	if f == nil || !f.enabled {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// Reset state for next utterance
	f.listening = false
	f.fillerRequested = false
	f.fillerReady = false
	f.fillerConsumed = false
	f.pendingFiller = ""
	f.consumedFiller = ""
	f.activeRequestToken = 0
	f.fillerUtteranceID = 0
}

func (f *fillerState) storeResult(token, utteranceID uint64, filler string, skip bool) {
	if f == nil || !f.enabled {
		return
	}
	trimmed := strings.TrimSpace(filler)
	f.mu.Lock()
	defer f.mu.Unlock()
	if token == 0 || token != f.activeRequestToken || utteranceID != f.fillerUtteranceID {
		return
	}
	f.activeRequestToken = 0
	if skip || trimmed == "" {
		f.pendingFiller = ""
		f.fillerReady = false
		return
	}
	f.pendingFiller = trimmed
	f.fillerReady = true
}

// waitForFiller polls until the filler result is ready or timeout elapses.
// Returns immediately (empty string) if no filler was requested for the
// current utterance (e.g. the user's speech was too short).
func (f *fillerState) waitForFiller(timeout time.Duration) string {
	if f == nil || !f.enabled {
		return ""
	}
	deadline := time.Now().Add(timeout)
	for {
		f.mu.Lock()
		// No filler was requested — nothing to wait for.
		if !f.fillerRequested {
			f.mu.Unlock()
			return ""
		}
		// Filler is ready and not yet consumed — grab it.
		if f.fillerReady && !f.fillerConsumed && f.pendingFiller != "" {
			filler := f.pendingFiller
			f.fillerConsumed = true
			f.consumedFiller = filler
			f.mu.Unlock()
			return filler
		}
		f.mu.Unlock()
		if time.Now().After(deadline) {
			return ""
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *fillerState) requestFailed(token, utteranceID uint64) {
	if f == nil || !f.enabled {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if token == 0 || token != f.activeRequestToken || utteranceID != f.fillerUtteranceID {
		return
	}
	f.activeRequestToken = 0
	f.fillerRequested = false
}
