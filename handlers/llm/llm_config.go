package llm

type LLMHandlerConfig struct {
	AllowToolCalls       bool
	BreakWords           []string
	PreEmptiveGeneration bool // Whether to enable pre-emptive generation of responses by the LLM handler. When enabled, the handler may generate responses in advance based on certain triggers or conditions.
}
