package factories

import (
	"errors"
	"interactkit/core"
	llmhandler "interactkit/handlers/llm"
	openaillm "interactkit/services/openai/llm"
)

// LLMFactoryConfig holds provider-specific configs for LLM service construction.
// Set exactly one provider config; the rest should be left nil.
// All non-OpenAI providers use the OpenAI-compatible protocol and are
// implemented via the same OpenAI service with a custom base URL.
type LLMFactoryConfig struct {
	OpenAIConfig     *openaillm.Config `json:"openai,omitempty"`
	TogetherConfig   *openaillm.Config `json:"together,omitempty"`
	GroqConfig       *openaillm.Config `json:"groq,omitempty"`
	DeepSeekConfig   *openaillm.Config `json:"deepseek,omitempty"`
	OpenRouterConfig *openaillm.Config `json:"openrouter,omitempty"`
	FireworksConfig  *openaillm.Config `json:"fireworks,omitempty"`
	CerebrasConfig   *openaillm.Config `json:"cerebras,omitempty"`
	XAIConfig        *openaillm.Config `json:"xai,omitempty"`
	MistralConfig    *openaillm.Config `json:"mistral,omitempty"`
	PerplexityConfig *openaillm.Config `json:"perplexity,omitempty"`
}

// Default base URLs for OpenAI-compatible providers.
const (
	togetherBaseURL   = "https://api.together.xyz/v1"
	groqBaseURL       = "https://api.groq.com/openai/v1"
	deepseekBaseURL   = "https://api.deepseek.com/v1"
	openrouterBaseURL = "https://openrouter.ai/api/v1"
	fireworksBaseURL  = "https://api.fireworks.ai/inference/v1"
	cerebrasBaseURL   = "https://api.cerebras.ai/v1"
	xaiBaseURL        = "https://api.x.ai/v1"
	mistralBaseURL    = "https://api.mistral.ai/v1"
	perplexityBaseURL = "https://api.perplexity.ai"
)

// BuildLLMService constructs an LLMService from the given factory config.
// Exactly one provider config must be non-nil.
func BuildLLMService(config LLMFactoryConfig, logger *core.Logger) (llmhandler.LLMService, error) {
	if config.OpenAIConfig != nil {
		return openaillm.NewOpenAILLMService(*config.OpenAIConfig, logger), nil
	}
	if config.TogetherConfig != nil {
		return buildOpenAICompatible(*config.TogetherConfig, togetherBaseURL, "meta-llama/Llama-3.3-70B-Instruct-Turbo", logger), nil
	}
	if config.GroqConfig != nil {
		return buildOpenAICompatible(*config.GroqConfig, groqBaseURL, "llama-3.3-70b-versatile", logger), nil
	}
	if config.DeepSeekConfig != nil {
		return buildOpenAICompatible(*config.DeepSeekConfig, deepseekBaseURL, "deepseek-chat", logger), nil
	}
	if config.OpenRouterConfig != nil {
		return buildOpenAICompatible(*config.OpenRouterConfig, openrouterBaseURL, "openai/gpt-4o", logger), nil
	}
	if config.FireworksConfig != nil {
		return buildOpenAICompatible(*config.FireworksConfig, fireworksBaseURL, "accounts/fireworks/models/llama-v3p3-70b-instruct", logger), nil
	}
	if config.CerebrasConfig != nil {
		return buildOpenAICompatible(*config.CerebrasConfig, cerebrasBaseURL, "llama-3.3-70b", logger), nil
	}
	if config.XAIConfig != nil {
		return buildOpenAICompatible(*config.XAIConfig, xaiBaseURL, "grok-3", logger), nil
	}
	if config.MistralConfig != nil {
		return buildOpenAICompatible(*config.MistralConfig, mistralBaseURL, "mistral-large-latest", logger), nil
	}
	if config.PerplexityConfig != nil {
		return buildOpenAICompatible(*config.PerplexityConfig, perplexityBaseURL, "sonar-pro", logger), nil
	}
	return nil, errors.New("LLMFactoryConfig: no provider config specified")
}

// buildOpenAICompatible creates an OpenAI-compatible LLM service, applying default
// base URL and model if not explicitly set in the config.
func buildOpenAICompatible(cfg openaillm.Config, defaultBaseURL, defaultModel string, logger *core.Logger) *openaillm.OpenAILLMService {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	return openaillm.NewOpenAILLMService(cfg, logger)
}
