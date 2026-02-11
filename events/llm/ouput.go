package llm

import "interactkit/core"

type LLMGenerateResponseEvent struct {
	Context core.LLMContext `json:"context"`
}

func (*LLMGenerateResponseEvent) GetId() string {
	return "llm.generate_response"
}

type LLMResponseStartedEvent struct {
}

func (e *LLMResponseStartedEvent) GetId() string {
	return "llm.response_started"
}

type LLMResponseChunkEvent struct {
	Chunk string // A chunk of the LLM response text.
}

func (e *LLMResponseChunkEvent) GetId() string {
	return "llm.response_chunk"
}

type LLMResponseCompletedEvent struct {
	FullText string // The complete LLM response text.
}

func (e *LLMResponseCompletedEvent) GetId() string {
	return "llm.response_completed"
}

type LLMToolInvocationRequestedEvent struct {
	ToolId string          // Identifier of the tool to be invoked.
	Params *map[string]any // Parameters required for the tool invocation.
}

func (e *LLMToolInvocationRequestedEvent) GetId() string {
	return "llm.tool_invocation_requested"
}
