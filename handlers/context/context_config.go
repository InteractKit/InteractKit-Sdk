package context

import "interactkit/core"

// LLMContextManagerConfig holds configuration for LLMContextManager construction
type LLMContextManagerConfig struct {
	AllowContinueListening bool `json:"allow_continue_listening"` // If true, the continue_listening tool is registered so the LLM can ask for more user input when the utterance appears incomplete.
	HumanLikeSpeech        bool `json:"human_like_speech"`        // If true, a human-like speech style prompt is appended to all system messages.
}

// DefaultLLMContextManagerConfig returns an LLMContextManagerConfig with sensible defaults
func DefaultLLMContextManagerConfig() LLMContextManagerConfig {
	return LLMContextManagerConfig{
		AllowContinueListening: true,
		HumanLikeSpeech:        true,
	}
}

// ContextConfig holds configuration for context handler initialization (keep-quiet tool, etc.)
type ContextConfig struct {
	AddKeepQuietTool      bool             `json:"add_keep_quiet_tool"`     // Whether to add a "keep quiet" tool to the LLM handler. This tool can be used to instruct the LLM to remain silent in certain situations.
	KeepQuietInstructions string           `json:"keep_quiet_instructions"` // Instructions for the "keep quiet" tool, guiding the LLM on when and how to use it.
	Context               *core.LLMContext `json:"context"`                 // The context to be used by the context handler.
}

// DefaultContextConfig returns a ContextConfig with sensible defaults
func DefaultContextConfig() ContextConfig {
	return ContextConfig{}
}
