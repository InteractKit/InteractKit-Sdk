package llm

// LLMHandlerConfig holds configuration for the LLM handler
type LLMHandlerConfig struct {
	AllowToolCalls       bool         `json:"allow_tool_calls"`
	BreakWords           []string     `json:"break_words"`
	PreEmptiveGeneration bool         `json:"pre_emptive_generation"`
	GenerateFillers      bool         `json:"generate_fillers"`
}

// DefaultConfig returns an LLMHandlerConfig with sensible defaults
func DefaultConfig() LLMHandlerConfig {
	return LLMHandlerConfig{
		AllowToolCalls:       true,
		PreEmptiveGeneration: true,
		GenerateFillers:      true,
	}
}
