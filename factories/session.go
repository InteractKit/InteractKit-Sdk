package factories

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"interactkit/core"
	contexthandler "interactkit/handlers/context"
	llmhandler "interactkit/handlers/llm"
	stthandler "interactkit/handlers/stt"
	ttshandler "interactkit/handlers/tts"
)

// SessionTTSConfig bundles TTS handler config with primary and optional fallback service factory configs.
type SessionTTSConfig struct {
	// HandlerConfig controls handler-level TTS behaviour (break words, buffer sizes, etc.).
	HandlerConfig ttshandler.TTSConfig `json:"handler"`
	// ServiceConfig selects and configures the primary TTS provider.
	// Set exactly one provider field inside TTSFactoryConfig.
	ServiceConfig TTSFactoryConfig `json:"service"`
	// FallbackServiceConfigs is an ordered list of fallback providers tried if the primary fails.
	FallbackServiceConfigs []TTSFactoryConfig `json:"fallbacks,omitempty"`
}

// DefaultSessionTTSConfig returns a SessionTTSConfig with sensible handler defaults.
// Populate ServiceConfig before calling BuildHandler or SessionConfig.BuildHandlers.
func DefaultSessionTTSConfig() SessionTTSConfig {
	return SessionTTSConfig{
		HandlerConfig: ttshandler.DefaultConfig(),
	}
}

// BuildHandler constructs a TTSHandler with primary and fallback services wired up.
func (c SessionTTSConfig) BuildHandler(logger *core.Logger) (*ttshandler.TTSHandler, error) {
	primary, err := BuildTTSService(c.ServiceConfig, logger)
	if err != nil {
		return nil, fmt.Errorf("tts primary service: %w", err)
	}
	handler := ttshandler.NewTTSHandler(primary, c.HandlerConfig, logger)
	for i, fbCfg := range c.FallbackServiceConfigs {
		fb, err := BuildTTSService(fbCfg, logger)
		if err != nil {
			return nil, fmt.Errorf("tts fallback[%d]: %w", i, err)
		}
		handler.BackupServices = append(handler.BackupServices, fb)
	}
	return handler, nil
}

// SessionSTTConfig bundles STT handler config with primary and optional fallback service factory configs.
type SessionSTTConfig struct {
	// HandlerConfig controls handler-level STT behaviour.
	HandlerConfig stthandler.STTConfig `json:"handler"`
	// ServiceConfig selects and configures the primary STT provider.
	// Set exactly one provider field inside STTFactoryConfig.
	ServiceConfig STTFactoryConfig `json:"service"`
	// FallbackServiceConfigs is an ordered list of fallback providers tried if the primary fails.
	FallbackServiceConfigs []STTFactoryConfig `json:"fallbacks,omitempty"`
}

// DefaultSessionSTTConfig returns a SessionSTTConfig with sensible handler defaults.
// Populate ServiceConfig before calling BuildHandler or SessionConfig.BuildHandlers.
func DefaultSessionSTTConfig() SessionSTTConfig {
	return SessionSTTConfig{
		HandlerConfig: stthandler.DefaultConfig(),
	}
}

// BuildHandler constructs an STTHandler with primary and fallback services wired up.
func (c SessionSTTConfig) BuildHandler(logger *core.Logger) (*stthandler.STTHandler, error) {
	primary, err := BuildSTTService(c.ServiceConfig, logger)
	if err != nil {
		return nil, fmt.Errorf("stt primary service: %w", err)
	}
	handler := stthandler.NewSTTHandler(primary, c.HandlerConfig, logger)
	for i, fbCfg := range c.FallbackServiceConfigs {
		fb, err := BuildSTTService(fbCfg, logger)
		if err != nil {
			return nil, fmt.Errorf("stt fallback[%d]: %w", i, err)
		}
		handler.WithBackupService(fb)
	}
	return handler, nil
}

// SessionLLMConfig bundles LLM handler config with primary, fallback, and optional filler service factory configs.
type SessionLLMConfig struct {
	// HandlerConfig controls handler-level LLM behaviour (tool calls, fillers, pre-emption, etc.).
	HandlerConfig llmhandler.LLMHandlerConfig `json:"handler"`
	// ServiceConfig selects and configures the primary LLM provider.
	// Set exactly one provider field inside LLMFactoryConfig.
	ServiceConfig LLMFactoryConfig `json:"service"`
	// FallbackServiceConfigs is an ordered list of fallback providers tried if the primary fails.
	FallbackServiceConfigs []LLMFactoryConfig `json:"fallbacks,omitempty"`
	// FillerServiceConfig optionally selects a lighter, cheaper model used only for filler
	// word generation. When nil, the primary service handles fillers too.
	FillerServiceConfig *LLMFactoryConfig `json:"filler_service,omitempty"`
}

// DefaultSessionLLMConfig returns a SessionLLMConfig with sensible handler defaults.
// Populate ServiceConfig before calling BuildHandler or SessionConfig.BuildHandlers.
func DefaultSessionLLMConfig() SessionLLMConfig {
	return SessionLLMConfig{
		HandlerConfig: llmhandler.DefaultConfig(),
	}
}

// BuildHandler constructs an LLMHandler with primary, fallback, and filler services wired up.
func (c SessionLLMConfig) BuildHandler(logger *core.Logger) (*llmhandler.LLMHandler, error) {
	primary, err := BuildLLMService(c.ServiceConfig, logger)
	if err != nil {
		return nil, fmt.Errorf("llm primary service: %w", err)
	}
	handler := llmhandler.NewLLMHandler(primary, c.HandlerConfig, logger)
	for i, fbCfg := range c.FallbackServiceConfigs {
		fb, err := BuildLLMService(fbCfg, logger)
		if err != nil {
			return nil, fmt.Errorf("llm fallback[%d]: %w", i, err)
		}
		handler.WithBackupService(fb)
	}
	if c.FillerServiceConfig != nil {
		filler, err := BuildLLMService(*c.FillerServiceConfig, logger)
		if err != nil {
			return nil, fmt.Errorf("llm filler service: %w", err)
		}
		handler.WithFillerService(filler)
	}
	return handler, nil
}

// SessionContextConfig bundles context-manager and handler-level context configs.
type SessionContextConfig struct {
	// ManagerConfig controls LLMContextManager behaviour (continue-listening tool,
	// human-like speech style, etc.).
	ManagerConfig contexthandler.LLMContextManagerConfig `json:"manager"`
	// HandlerConfig holds the initial LLM context (system messages + tools) and optional
	// keep-quiet tool settings applied when BuildHandlers is called.
	HandlerConfig contexthandler.ContextConfig `json:"handler"`
	// TaskGroup is an optional dynamic task group loaded into the context manager at
	// startup. When nil the context manager starts with no task group.
	TaskGroup *contexthandler.TaskGroupConfig `json:"task_group,omitempty"`
	// MCPServers is an optional list of MCP servers to connect to at startup.
	// Their tools are discovered and registered as standard InteractKit tools.
	MCPServers []core.MCPServerConfig `json:"mcp_servers,omitempty"`
}

// DefaultSessionContextConfig returns a SessionContextConfig with sensible defaults.
func DefaultSessionContextConfig() SessionContextConfig {
	return SessionContextConfig{
		ManagerConfig: contexthandler.DefaultLLMContextManagerConfig(),
		HandlerConfig: contexthandler.DefaultContextConfig(),
	}
}

// SessionConfig is the top-level configuration for a complete voice-AI session pipeline.
// It groups TTS, STT, LLM, and context configs — each with primary and fallback services —
// and exposes BuildHandlers to construct all ready-to-wire handlers in a single call.
type SessionConfig struct {
	TTS     SessionTTSConfig     `json:"tts"`
	STT     SessionSTTConfig     `json:"stt"`
	LLM     SessionLLMConfig     `json:"llm"`
	Context SessionContextConfig `json:"context"`
}

// DefaultSessionConfig returns a SessionConfig pre-filled with sensible handler defaults
// for every component. Populate the ServiceConfig fields before calling BuildHandlers.
func DefaultSessionConfig() SessionConfig {
	return SessionConfig{
		TTS:     DefaultSessionTTSConfig(),
		STT:     DefaultSessionSTTConfig(),
		LLM:     DefaultSessionLLMConfig(),
		Context: DefaultSessionContextConfig(),
	}
}

// SessionConfigFromJSON parses a JSON blob into a SessionConfig, starting from
// DefaultSessionConfig so that any fields absent from the JSON retain their defaults.
// API keys and other secrets should be injected after loading via env vars rather than
// stored in config files.
func SessionConfigFromJSON(data []byte) (SessionConfig, error) {
	cfg := DefaultSessionConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return SessionConfig{}, fmt.Errorf("session config: %w", err)
	}
	return cfg, nil
}

// APIKeys holds API credentials for all supported service providers.
// Pass to SessionConfig.InjectAPIKeys after loading from JSON so that
// secrets are never stored in config files.
type APIKeys struct {
	Deepgram   string // Used for Deepgram STT and TTS providers.
	OpenAI     string // Used for OpenAI LLM provider.
	Together   string // Used for Together AI LLM provider.
	Groq       string // Used for Groq LLM provider.
	DeepSeek   string // Used for DeepSeek LLM provider.
	OpenRouter string // Used for OpenRouter LLM provider.
	Fireworks  string // Used for Fireworks AI LLM provider.
	Cerebras   string // Used for Cerebras LLM provider.
	XAI        string // Used for xAI (Grok) LLM provider.
	Mistral    string // Used for Mistral AI LLM provider.
	Perplexity string // Used for Perplexity LLM provider.
	ElevenLabs string // Used for ElevenLabs TTS provider.
	Cartesia   string // Used for Cartesia TTS provider.
}

// InjectAPIKeys applies API credentials to all configured service providers
// (primary and fallbacks) in the SessionConfig. Call this after loading from
// JSON so that secrets are not stored in config files.
func (c *SessionConfig) InjectAPIKeys(keys APIKeys) {
	// STT
	if c.STT.ServiceConfig.DeepgramConfig != nil && c.STT.ServiceConfig.DeepgramConfig.APIKey == "" {
		c.STT.ServiceConfig.DeepgramConfig.APIKey = keys.Deepgram
	}
	for i := range c.STT.FallbackServiceConfigs {
		if c.STT.FallbackServiceConfigs[i].DeepgramConfig != nil && c.STT.FallbackServiceConfigs[i].DeepgramConfig.APIKey == "" {
			c.STT.FallbackServiceConfigs[i].DeepgramConfig.APIKey = keys.Deepgram
		}
	}

	// LLM
	injectLLMKeys(&c.LLM.ServiceConfig, keys)
	for i := range c.LLM.FallbackServiceConfigs {
		injectLLMKeys(&c.LLM.FallbackServiceConfigs[i], keys)
	}
	if c.LLM.FillerServiceConfig != nil {
		injectLLMKeys(c.LLM.FillerServiceConfig, keys)
	}

	// TTS (primary + fallbacks)
	injectTTSKeys(&c.TTS.ServiceConfig, keys)
	for i := range c.TTS.FallbackServiceConfigs {
		injectTTSKeys(&c.TTS.FallbackServiceConfigs[i], keys)
	}
}

// injectLLMKeys applies the relevant API key to a single LLMFactoryConfig.
func injectLLMKeys(cfg *LLMFactoryConfig, keys APIKeys) {
	if cfg.OpenAIConfig != nil && cfg.OpenAIConfig.APIKey == "" {
		cfg.OpenAIConfig.APIKey = keys.OpenAI
	}
	if cfg.TogetherConfig != nil && cfg.TogetherConfig.APIKey == "" {
		cfg.TogetherConfig.APIKey = keys.Together
	}
	if cfg.GroqConfig != nil && cfg.GroqConfig.APIKey == "" {
		cfg.GroqConfig.APIKey = keys.Groq
	}
	if cfg.DeepSeekConfig != nil && cfg.DeepSeekConfig.APIKey == "" {
		cfg.DeepSeekConfig.APIKey = keys.DeepSeek
	}
	if cfg.OpenRouterConfig != nil && cfg.OpenRouterConfig.APIKey == "" {
		cfg.OpenRouterConfig.APIKey = keys.OpenRouter
	}
	if cfg.FireworksConfig != nil && cfg.FireworksConfig.APIKey == "" {
		cfg.FireworksConfig.APIKey = keys.Fireworks
	}
	if cfg.CerebrasConfig != nil && cfg.CerebrasConfig.APIKey == "" {
		cfg.CerebrasConfig.APIKey = keys.Cerebras
	}
	if cfg.XAIConfig != nil && cfg.XAIConfig.APIKey == "" {
		cfg.XAIConfig.APIKey = keys.XAI
	}
	if cfg.MistralConfig != nil && cfg.MistralConfig.APIKey == "" {
		cfg.MistralConfig.APIKey = keys.Mistral
	}
	if cfg.PerplexityConfig != nil && cfg.PerplexityConfig.APIKey == "" {
		cfg.PerplexityConfig.APIKey = keys.Perplexity
	}
}

// injectTTSKeys applies the relevant API key to a single TTSFactoryConfig.
func injectTTSKeys(cfg *TTSFactoryConfig, keys APIKeys) {
	if cfg.DeepgramConfig != nil && cfg.DeepgramConfig.APIKey == "" {
		cfg.DeepgramConfig.APIKey = keys.Deepgram
	}
	if cfg.ElevenLabsConfig != nil && cfg.ElevenLabsConfig.APIKey == "" {
		cfg.ElevenLabsConfig.APIKey = keys.ElevenLabs
	}
	if cfg.CartesiaConfig != nil && cfg.CartesiaConfig.APIKey == "" {
		cfg.CartesiaConfig.APIKey = keys.Cartesia
	}
}

// SessionHandlers holds all constructed handlers ready to be assembled into a Runner pipeline.
//
// Typical pipeline order:
//
//	TransportInput → VAD → STT → ContextManager.GetUserContextAggregator()
//	  → LLM → ContextManager.GetAssistantContextAggregator() → TTS → … → TransportOutput
type SessionHandlers struct {
	TTS *ttshandler.TTSHandler
	STT *stthandler.STTHandler
	LLM *llmhandler.LLMHandler
	// ContextManager manages conversation state. Call GetUserContextAggregator() and
	// GetAssistantContextAggregator() to obtain the two pipeline handlers.
	ContextManager *contexthandler.DynamicContextManager
}

// BuildHandlers constructs all handlers described by the SessionConfig.
// Returns an error if any service factory config is invalid or construction fails.
func (c SessionConfig) BuildHandlers(logger *core.Logger) (*SessionHandlers, error) {
	ttsHandler, err := c.TTS.BuildHandler(logger)
	if err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}

	sttHandler, err := c.STT.BuildHandler(logger)
	if err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}

	llmHandler, err := c.LLM.BuildHandler(logger)
	if err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}

	baseCtxMgr := contexthandler.NewLLMContextManager(c.Context.ManagerConfig, logger)
	if c.Context.HandlerConfig.Context != nil {
		baseCtxMgr.SetContext(c.Context.HandlerConfig.Context)
	}

	ctxMgr := contexthandler.NewDynamicContextManager(baseCtxMgr)

	// MCP servers: connect, discover tools, and register them.
	if len(c.Context.MCPServers) > 0 {
		mcpMgr := contexthandler.NewMCPManager(logger)
		connectCtx, connectCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer connectCancel()

		if err := mcpMgr.Connect(connectCtx, c.Context.MCPServers); err != nil {
			logger.With(map[string]any{"error": err.Error()}).Error("MCP connection errors (non-fatal)")
		}

		for _, tool := range mcpMgr.Tools() {
			toolID := tool.ToolId
			ctxMgr.RegisterTool(tool, func(call core.LLMToolCall) string {
				params := make(map[string]any)
				if call.Parameters != nil {
					params = *call.Parameters
				}
				result, err := mcpMgr.CallTool(context.Background(), toolID, params)
				if err != nil {
					logger.With(map[string]any{
						"tool":  toolID,
						"error": err.Error(),
					}).Error("MCP tool call failed")
					return "Error: " + err.Error()
				}
				return result
			})
		}

		baseCtxMgr.SetCleanupHook(func() {
			mcpMgr.Close()
		})
	}

	if c.Context.TaskGroup != nil {
		if err := ctxMgr.LoadTaskGroup(c.Context.TaskGroup); err != nil {
			return nil, fmt.Errorf("session: load task group: %w", err)
		}
	}

	if c.Context.HandlerConfig.AddKeepQuietTool {
		instructions := c.Context.HandlerConfig.KeepQuietInstructions
		if instructions == "" {
			instructions = "Remain silent and do not respond. Wait for the user to speak."
		}
		ctxMgr.RegisterTool(core.LLMTool{
			ToolId:      "keep_quiet",
			Name:        "keep_quiet",
			Description: instructions,
			Parameters:  []core.Parameter{},
		}, func(_ core.LLMToolCall) string {
			return ""
		})
	}

	return &SessionHandlers{
		TTS:            ttsHandler,
		STT:            sttHandler,
		LLM:            llmHandler,
		ContextManager: ctxMgr,
	}, nil
}
