package context

import "interactkit/core"

type ContextConfig struct {
	AddKeepQuietTool      bool             // Whether to add a "keep quiet" tool to the LLM handler. This tool can be used to instruct the LLM to remain silent in certain situations.
	KeepQuietInstructions string           // Instructions for the "keep quiet" tool, guiding the LLM on when and how to use it.
	Context               *core.LLMContext // The context to be used by the context handler.
	TraversalDefinition   *core.AgentDefinition
}
