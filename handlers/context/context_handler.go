package context

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"interactkit/core"
	"interactkit/events/llm"
	"interactkit/events/stt"
	"interactkit/events/tts"
	"interactkit/events/vad"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

type ToolHandlerFunc func(toolInput core.LLMToolCall) string

// VariableToolInfo holds the metadata needed to generate filler and
// confirmation phrases when a set_* variable tool is called.
type VariableToolInfo struct {
	Description    string
	AutoConfirm    bool
	ConfirmPhrases []string // per-variable overrides; nil = use global pool
}

// DummyContextService is a no-op service for context handlers
type DummyContextService struct{}

func (s *DummyContextService) Initialize(ctx context.Context) error {
	return nil
}

func (s *DummyContextService) Cleanup() error {
	return nil
}

func (s *DummyContextService) Reset() error {
	return nil
}

var continueListeningAcks = []string{
	"Uh-huh?...",
	"Mm-hmm?...",
	"Yeah?...",
}

func randomAck() string {
	return continueListeningAcks[rand.Intn(len(continueListeningAcks))]
}

const ContinueListeningToolId = "continue_listening"

var ContinueListeningTool = core.LLMTool{
	ToolId:      ContinueListeningToolId,
	Description: "Trigger ONLY when the user's message is clearly incomplete, cut-off mid-sentence, or trailing (e.g., ends with an ellipsis, a partial word, or an unfinished clause). Do NOT trigger for short but complete messages like one-word answers ('Yes', 'No', 'Thanks') or typical conversational fragments that are semantically self-contained. If unsure, default to false.",
	Parameters: []core.Parameter{
		{
			Name:        "allow_listen",
			Description: "Set to true to continue listening for more user input when the message appears incomplete.",
			Required:    true,
			Type:        "boolean",
		},
	},
}

const EndCallToolId = "end_call"

// EndCallTool is always registered in the base context manager.
// The LLM should call it after delivering closing words — never mid-sentence.
var EndCallTool = core.LLMTool{
	ToolId:      EndCallToolId,
	Name:        EndCallToolId,
	Description: "End the call after you have finished your closing words. Call this only when the conversation is fully complete and the caller has been properly served or transferred.",
	Parameters: []core.Parameter{
		{
			Name:        "reason",
			Description: "Brief reason the call is ending (e.g. 'estimate scheduled', 'transferred to staff', 'caller hung up').",
			Required:    false,
			Type:        core.LLMParameterTypeString,
		},
	},
}

// EventToolHandlerFunc is called when the LLM invokes an "event tool".
// It returns a brief string result (added to context) and the pipeline event
// that should be fired. No new LLM generation is triggered after this — the
// event itself drives what happens next (e.g. EndCallEvent stops the runner).
type EventToolHandlerFunc func(toolInput core.LLMToolCall) (result string, event core.IEvent)

type LLMContextManager struct {
	ToolCallRunnerLLM      core.IService
	Logger                 *core.Logger
	context                *core.LLMContext
	toolHandlers           map[string]ToolHandlerFunc
	eventToolHandlers      map[string]EventToolHandlerFunc
	variableTools          map[string]VariableToolInfo // set_* tools registered by DynamicContextManager
	allowContinueListening bool
	humanLikeSpeech        bool
	cleanupHook            func()           // optional; called by LLMAssistantContextAggregator.Cleanup()
	eventEmitter           func(core.IEvent) // optional; routes IExternalOutputEvent into the pipeline
}

// registerVariableTool records metadata for a set_* variable tool so that
// HandleToolCall can generate filler and confirmation phrases for it.
func (m *LLMContextManager) registerVariableTool(toolID string, info VariableToolInfo) {
	if m.variableTools == nil {
		m.variableTools = make(map[string]VariableToolInfo)
	}
	m.variableTools[toolID] = info
}

// findToolByID returns the LLMTool definition for the given tool ID, or nil if not found.
func (m *LLMContextManager) findToolByID(toolID string) *core.LLMTool {
	if m.context == nil {
		return nil
	}
	for i := range m.context.Tools {
		if m.context.Tools[i].ToolId == toolID {
			return &m.context.Tools[i]
		}
	}
	return nil
}

// callServerTool makes an HTTP POST to the given URL with the tool call parameters
// and returns the response body as a string result for the LLM.
func (m *LLMContextManager) callServerTool(serverURL string, toolCall core.LLMToolCall) string {
	payload, err := json.Marshal(toolCall)
	if err != nil {
		m.Logger.Errorf("LLM Context Manager: Failed to marshal tool call for %s: %v", toolCall.ToolId, err)
		return "Error: failed to prepare tool call request"
	}

	resp, err := http.Post(serverURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		m.Logger.Errorf("LLM Context Manager: HTTP request to %s failed: %v", serverURL, err)
		return "Error: failed to reach tool server"
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		m.Logger.Errorf("LLM Context Manager: Failed to read response from %s: %v", serverURL, err)
		return "Error: failed to read tool server response"
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		m.Logger.Errorf("LLM Context Manager: Tool server %s returned status %d: %s", serverURL, resp.StatusCode, string(body))
		return fmt.Sprintf("Error: tool server returned status %d", resp.StatusCode)
	}

	return string(body)
}

func (m *LLMContextManager) SetContext(ctx *core.LLMContext) {
	if m.allowContinueListening {
		hasListenTool := false
		for _, tool := range ctx.Tools {
			if tool.ToolId == ContinueListeningToolId {
				hasListenTool = true
				break
			}
		}
		if !hasListenTool {
			ctx.Tools = append(ctx.Tools, ContinueListeningTool)
		}
	}

	// end_call is always available — inject it if not already present.
	hasEndCallTool := false
	for _, tool := range ctx.Tools {
		if tool.ToolId == EndCallToolId {
			hasEndCallTool = true
			break
		}
	}
	if !hasEndCallTool {
		ctx.Tools = append(ctx.Tools, EndCallTool)
	}

	if m.humanLikeSpeech {
		for i, msg := range ctx.Messages {
			if msg.Role == "system" {
				ctx.Messages[i].Message += "\n\n" + HUMAN_LIKE_RESPONSE_PROMPT
			}
		}
	}
	m.context = ctx
}

func (m *LLMContextManager) LoadContextJson(jsonData []byte) error {
	var ctx core.LLMContext
	err := json.Unmarshal(jsonData, &ctx)
	if err != nil {
		return err
	}
	m.SetContext(&ctx)
	return nil
}

func (m *LLMContextManager) GetContext() *core.LLMContext {
	return m.context
}

func (m *LLMContextManager) RegisterToolHandler(toolName string, handler ToolHandlerFunc) {
	if m.toolHandlers == nil {
		m.toolHandlers = make(map[string]ToolHandlerFunc)
	}
	m.toolHandlers[toolName] = handler
}

// RegisterEventToolHandler registers a tool whose invocation fires a pipeline
// event instead of triggering another LLM generation. Use for terminal actions
// like ending the call or transferring to a human.
func (m *LLMContextManager) RegisterEventToolHandler(toolName string, handler EventToolHandlerFunc) {
	if m.eventToolHandlers == nil {
		m.eventToolHandlers = make(map[string]EventToolHandlerFunc)
	}
	m.eventToolHandlers[toolName] = handler
}

func (m *LLMContextManager) GetUserContextAggregator() *LLMUserContextAggregator {
	dummyService := &DummyContextService{}
	return &LLMUserContextAggregator{
		BaseHandler:    *core.NewBaseHandler(dummyService, nil, context.Background(), m.Logger),
		contextManager: m,
	}
}

func (m *LLMContextManager) GetAssistantContextAggregator() *LLMAssistantContextAggregator {
	dummyService := &DummyContextService{}
	return &LLMAssistantContextAggregator{
		BaseHandler:    *core.NewBaseHandler(dummyService, nil, context.Background(), m.Logger),
		contextManager: m,
	}
}

func (p *LLMContextManager) AddToolDefinitions(tools []core.LLMTool) {
	p.context.Tools = append(p.context.Tools, tools...)
}

type LLMUserContextAggregator struct {
	core.BaseHandler
	contextManager              *LLMContextManager
	falsePositiveTimer          *time.Timer
	falsePositiveRegenerateChan chan *core.LLMContext
}

func (p *LLMUserContextAggregator) Init(
	inputChan <-chan *core.EventPacket,
	outputNextChan chan<- *core.EventPacket,
	outputTopChan chan<- *core.EventPacket,
	ctx context.Context,
) error {
	p.falsePositiveRegenerateChan = make(chan *core.LLMContext, 1)
	return p.BaseHandler.Initialize(inputChan, outputNextChan, outputTopChan, ctx)
}

func (p *LLMUserContextAggregator) Start() error {
	go p.eventLoop()
	return nil
}

func (p *LLMUserContextAggregator) eventLoop() {
	for {
		select {
		case <-p.Ctx.Done():
			return
		case eventPacket := <-p.InputChan:
			p.HandleEvent(eventPacket)
		case ctx := <-p.falsePositiveRegenerateChan:
			// No STT interim arrived within the timeout — treat as false positive,
			// regenerate the response with the context captured at interruption time.
			p.Logger.Info("False positive VAD interruption detected, regenerating LLM response")
			p.SendPacket(core.NewEventPacket(
				&llm.LLMGenerateResponseEvent{Context: *ctx},
				core.EventRelayDestinationNextService,
				"LLMContextManager",
			))
		}
	}
}

func (p *LLMUserContextAggregator) stopFalsePositiveTimer() {
	if p.falsePositiveTimer != nil {
		if !p.falsePositiveTimer.Stop() {
			// Timer already fired; drain the channel so a stale regeneration
			// signal doesn't fire after real speech arrives.
			select {
			case <-p.falsePositiveRegenerateChan:
			default:
			}
		}
		p.falsePositiveTimer = nil
	}
}

func (p *LLMUserContextAggregator) HandleEvent(eventPacket *core.EventPacket) error {
	switch event := eventPacket.Event.(type) {

	case *vad.VadInterruptionSuspectedEvent:
		// Stop any previous timer and start a fresh 2-second window.
		// If no STT interim arrives within that window we assume it was a
		// false positive (background noise) and regenerate the response.
		p.stopFalsePositiveTimer()
		if p.contextManager.context != nil {
			capturedCtx := *p.contextManager.context // value copy — no data race
			p.falsePositiveTimer = time.AfterFunc(2*time.Second, func() {
				select {
				case p.falsePositiveRegenerateChan <- &capturedCtx:
				default:
				}
			})
		}

	case *stt.STTInterimOutputEvent:
		if p.falsePositiveTimer != nil {
			// STT interim during suspected state — confirms the interruption is real.
			p.stopFalsePositiveTimer()
			p.SendPacket(core.NewEventPacket(
				&vad.VadInterruptionConfirmedEvent{},
				core.EventRelayDestinationNextService,
				"LLMContextManager",
			))
		}
		_ = event // event forwarded below
		// create a generate interim event with the partial transcript to update fillers in real-time
		p.SendPacket(core.NewEventPacket(
			&llm.LLMPrepareInterimFillerEvent{
				PartialTranscript: event.Text,
				Context:           p.contextManager.context,
			},
			core.EventRelayDestinationNextService,
			"LLMContextManager",
		))

	case *stt.STTFinalOutputEvent:
		// Real speech confirmed — cancel any pending false-positive timer first.
		p.stopFalsePositiveTimer()

		if p.contextManager.context == nil {
			p.contextManager.context = &core.LLMContext{
				Messages: []core.LLMMessage{},
			}
		}
		p.contextManager.context.Messages = append(
			p.contextManager.context.Messages,
			core.LLMMessage{
				Role:    "user",
				Message: event.Text,
			},
		)
		p.Logger.Info("LLM Context Manager: Added user message to context.")

		// Generate LLM response
		generateEvent := &llm.LLMGenerateResponseEvent{
			Context: *p.contextManager.context,
		}
		p.SendPacket(core.NewEventPacket(generateEvent, core.EventRelayDestinationNextService, "LLMContextManager"))
		p.Logger.Info("LLM Context Manager: Triggered LLM response generation.")
	}

	// Forward the event
	p.SendPacket(eventPacket)
	return nil
}

func (p *LLMUserContextAggregator) Cleanup() error {
	return p.BaseHandler.Cleanup()
}

func (p *LLMUserContextAggregator) Reset() error {
	return p.BaseHandler.Reset()
}

type LLMAssistantContextAggregator struct {
	core.BaseHandler
	contextManager *LLMContextManager
	spokenBuffer   string // accumulates spoken words for the current turn
	hasActiveTurn  bool   // true between LLMResponseStarted and LLMResponseCompleted/interruption
	interrupted    bool   // true if the current turn was interrupted by VAD
}

func (p *LLMAssistantContextAggregator) Init(
	inputChan <-chan *core.EventPacket,
	outputNextChan chan<- *core.EventPacket,
	outputTopChan chan<- *core.EventPacket,
	ctx context.Context,
) error {
	err := p.BaseHandler.Initialize(inputChan, outputNextChan, outputTopChan, ctx)
	p.contextManager.eventEmitter = func(event core.IEvent) {
		p.SendPacket(core.NewEventPacket(event, core.EventRelayDestinationTopService, "DynamicContextManager"))
	}
	return err
}

func (p *LLMAssistantContextAggregator) Start() error {
	go p.eventLoop()
	return nil
}

func (p *LLMAssistantContextAggregator) eventLoop() {
	for {
		select {
		case <-p.Ctx.Done():
			return
		case eventPacket := <-p.InputChan:
			p.HandleEvent(eventPacket)
		}
	}
}

func (p *LLMAssistantContextAggregator) HandleEvent(eventPacket *core.EventPacket) error {
	switch event := eventPacket.Event.(type) {
	case *llm.LLMToolInvocationRequestedEvent:
		p.Logger.Infof("LLM Context Manager: Tool call requested: %s", event.ToolId)
		if event.ToolId == ContinueListeningToolId {
			p.Logger.Info("LLM Context Manager: Continue listening requested.")
			ack := randomAck()
			p.Logger.Infof("LLM Context Manager: Sending ack: %q", ack)
			p.SendPacket(core.NewEventPacket(
				&tts.TTSSpeakEvent{Text: ack},
				core.EventRelayDestinationNextService,
				"LLMContextManager",
			))
		} else {
			go p.HandleToolCall(event.ToolId, event.Params)
		}

	case *llm.LLMResponseStartedEvent:
		// Reset per-turn state for the new response.
		p.spokenBuffer = ""
		p.interrupted = false
		p.hasActiveTurn = true

	case *tts.TTSSpokenTextChunkEvent:
		// Accumulate spoken words locally; don't write to context yet.
		// If the turn was already interrupted the buffer was already committed.
		if p.hasActiveTurn && !p.interrupted {
			if len(p.spokenBuffer) > 0 && len(event.Text) > 0 &&
				!isWhitespace(p.spokenBuffer[len(p.spokenBuffer)-1]) &&
				!isWhitespace(event.Text[0]) {
				p.spokenBuffer += " "
			}
			p.spokenBuffer += event.Text
		}

	case *vad.VadInterruptionConfirmedEvent:
		// User interruption confirmed — commit only what was actually spoken so far.
		if p.hasActiveTurn {
			p.interrupted = true
			p.commitAssistantMessage(p.spokenBuffer)
			p.spokenBuffer = ""
			p.hasActiveTurn = false
			p.Logger.Info("LLM Context Manager: Committed partial assistant message after confirmed interruption.")
		}

	case *llm.LLMResponseCompletedEvent:
		// Response finished without interruption — use the authoritative full text
		// so timing gaps in the word-by-word simulation can never cause a miss.
		if p.hasActiveTurn && !p.interrupted {
			p.commitAssistantMessage(event.FullText)
			p.Logger.Infof("LLM Context Manager: Committed full assistant message (%d chars).", len(event.FullText))
		}
		p.spokenBuffer = ""
		p.hasActiveTurn = false

	case *llm.LLMToolInvocationResultEvent:
		p.Logger.Infof("LLM Context Manager: Tool call result received for tool %s: %s", event.ToolId, event.Result)
		// Add the tool result to context as a system message, so the LLM can condition its next response on it.
		if p.contextManager.context != nil && event.Result != "" {
			p.contextManager.context.Messages = append(
				p.contextManager.context.Messages,
				core.LLMMessage{
					Role:    "system",
					Message: "Tool " + event.ToolId + " result: " + event.Result,
				},
			)
			p.Logger.Infof("LLM Context Manager: Added tool result to context for tool %s.", event.ToolId)
		}
		// Trigger a new LLM response to react to the tool result, unless this was a continue_listening signal which doesn't require an immediate response.
		if event.ToolId != ContinueListeningToolId {
			generateEvent := &llm.LLMGenerateResponseEvent{
				Context: *p.contextManager.context,
			}
			p.SendPacket(core.NewEventPacket(generateEvent, core.EventRelayDestinationTopService, "LLMContextManager"))
			p.Logger.Infof("LLM Context Manager: Triggered LLM response generation after tool result for tool %s.", event.ToolId)
		}
	}

	// Forward the event
	p.SendPacket(eventPacket)
	return nil
}

// commitAssistantMessage appends text as a new assistant message to the context.
func (p *LLMAssistantContextAggregator) commitAssistantMessage(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if p.contextManager.context == nil {
		p.contextManager.context = &core.LLMContext{
			Messages: []core.LLMMessage{},
		}
	}
	p.contextManager.context.Messages = append(
		p.contextManager.context.Messages,
		core.LLMMessage{
			Role:    "assistant",
			Message: text,
		},
	)
}

func (p *LLMAssistantContextAggregator) HandleToolCall(toolId string, params *map[string]any) {
	toolCall := core.LLMToolCall{
		ToolId:     toolId,
		Parameters: params,
	}

	// ── Event tools: fire a pipeline event, do NOT re-trigger LLM generation ──
	if eventHandler, isEventTool := p.contextManager.eventToolHandlers[toolId]; isEventTool {
		p.Logger.Infof("LLM Context Manager: Invoking event tool handler for tool: %s", toolId)
		result, event := eventHandler(toolCall)
		p.Logger.Infof("LLM Context Manager: Event tool %s completed with result: %s", toolId, result)

		if p.contextManager.context != nil && result != "" {
			p.contextManager.context.Messages = append(
				p.contextManager.context.Messages,
				core.LLMMessage{
					Role:    "system",
					Message: "Tool " + toolId + " result: " + result,
				},
			)
		}
		if event != nil {
			p.SendPacket(core.NewEventPacket(event, core.EventRelayDestinationTopService, "LLMContextManager"))
			p.Logger.Infof("LLM Context Manager: Fired pipeline event %s from tool %s", event.GetId(), toolId)
		}
		return // intentionally no LLM regeneration
	}

	// ── Server URL tools: forward to external HTTP endpoint ──────────────────
	if tool := p.contextManager.findToolByID(toolId); tool != nil && tool.ServerURL != "" {
		p.Logger.Infof("LLM Context Manager: Forwarding tool %s to server URL: %s", toolId, tool.ServerURL)
		go func() {
			result := p.contextManager.callServerTool(tool.ServerURL, toolCall)
			p.SendPacket(core.NewEventPacket(
				&llm.LLMToolInvocationResultEvent{ToolId: toolId, Result: result},
				core.EventRelayDestinationTopService, "LLMContextManager",
			))
		}()
		return
	}

	// ── Standard tools: return a result and re-trigger LLM generation ─────────
	toolHandler, exists := p.contextManager.toolHandlers[toolId]
	if !exists {
		p.Logger.Infof("LLM Context Manager: No handler registered for tool: %s", toolId)
		return
	}

	p.Logger.Infof("LLM Context Manager: Invoking tool handler for tool: %s", toolId)
	result := toolHandler(toolCall)
	p.Logger.Infof("LLM Context Manager: Tool %s call completed with result: %s", toolId, result)

	// ── AutoConfirm variable tools: speak confirmation phrase directly via TTS ──
	if info, isVar := p.contextManager.variableTools[toolId]; isVar && info.AutoConfirm && !isVariableToolError(result) {
		value := extractParam(toolCall, "value")
		confirmText := randomVariableConfirmPhrase(info.Description, value, info.ConfirmPhrases)
		p.Logger.Infof("LLM Context Manager: speaking autoconfirm phrase for %s: %q", toolId, confirmText)

		p.SendPacket(core.NewEventPacket(
			&llm.LLMVariableConfirmRequestEvent{ConfirmText: confirmText},
			core.EventRelayDestinationTopService,
			"LLMContextManager",
		))
		p.commitAssistantMessage(confirmText)

		// Write tool result to context so the LLM knows the value is stored
		// when the caller's confirmation response eventually triggers regen.
		if p.contextManager.context != nil {
			p.contextManager.context.Messages = append(
				p.contextManager.context.Messages,
				core.LLMMessage{
					Role:    "system",
					Message: "Tool " + toolId + " result: " + result + " Waiting for caller to confirm.",
				},
			)
		}
		return // no LLM regen — wait for caller's verbal confirmation
	}

	// Standard path: fire result event → context aggregator adds it to context
	// and triggers a new LLM generation.
	finishEvent := &llm.LLMToolInvocationResultEvent{
		ToolId: toolId,
		Result: result,
	}
	p.SendPacket(core.NewEventPacket(finishEvent, core.EventRelayDestinationTopService, "LLMContextManager"))
}

// isVariableToolError returns true when a set_* tool result indicates a
// validation failure. In those cases we skip the filler/confirm phrase.
func isVariableToolError(result string) bool {
	return strings.HasPrefix(result, "Invalid") ||
		strings.Contains(result, "is not expected in any currently active task") ||
		strings.Contains(result, "No task group is currently loaded")
}

// SetCleanupHook sets a function that will be called when the assistant
// context aggregator is cleaned up (e.g. pipeline shutdown). If a hook is
// already set, the new hook chains after the existing one.
func (m *LLMContextManager) SetCleanupHook(fn func()) {
	prev := m.cleanupHook
	if prev == nil {
		m.cleanupHook = fn
	} else {
		m.cleanupHook = func() {
			prev()
			fn()
		}
	}
}

func (p *LLMAssistantContextAggregator) Cleanup() error {
	p.Logger.Info("LLM Assistant Context Aggregator cleaned up")
	if p.contextManager.cleanupHook != nil {
		p.contextManager.cleanupHook()
	}
	return p.BaseHandler.Cleanup()
}

func (p *LLMAssistantContextAggregator) Reset() error {
	return p.BaseHandler.Reset()
}

// NewLLMContextManager creates a new LLM context manager.
// Use DefaultLLMContextManagerConfig() to get a config with sensible defaults and override only what you need.
func NewLLMContextManager(config LLMContextManagerConfig, logger *core.Logger) *LLMContextManager {
	if logger == nil {
		logger = core.GetLogger()
	}

	toolHandlers := make(map[string]ToolHandlerFunc)
	if config.AllowContinueListening {
		toolHandlers[ContinueListeningToolId] = func(toolInput core.LLMToolCall) string {
			return "Continue listening signal sent."
		}
	}

	eventToolHandlers := make(map[string]EventToolHandlerFunc)
	eventToolHandlers[EndCallToolId] = func(toolInput core.LLMToolCall) (string, core.IEvent) {
		reason := "not specified"
		if toolInput.Parameters != nil {
			if v, ok := (*toolInput.Parameters)["reason"]; ok {
				reason = strings.TrimSpace(strings.Replace(fmt.Sprintf("%v", v), "\n", " ", -1))
			}
		}
		logger.With(map[string]any{"reason": reason}).Info("end_call tool invoked")
		return "Call ended: " + reason, &core.EndCallEvent{Reason: reason}
	}

	return &LLMContextManager{
		Logger:                 logger,
		toolHandlers:           toolHandlers,
		eventToolHandlers:      eventToolHandlers,
		allowContinueListening: config.AllowContinueListening,
		humanLikeSpeech:        config.HumanLikeSpeech,
		context: &core.LLMContext{
			Messages: []core.LLMMessage{},
			Tools:    []core.LLMTool{},
		},
	}
}

func isWhitespace(r byte) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}
