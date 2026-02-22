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
	Chunk              string // A chunk of the LLM response text.
	ConsumeImmediately bool   // If true, the consumer should speak this chunk immediately without buffering.
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

type LLMToolInvocationResultEvent struct {
	ToolId string // Identifier of the tool that was invoked.
	Result string // Result returned from the tool invocation, typically as a string.
}

func (e *LLMToolInvocationResultEvent) GetId() string {
	return "llm.tool_invocation_result"
}

type LLMPrepareInterimFillerEvent struct {
	PartialTranscript string           // The current partial transcript from STT to help generate context-aware fillers.
	Context           *core.LLMContext // The current LLM context, which may include previous messages and system instructions.
}

func (e *LLMPrepareInterimFillerEvent) GetId() string {
	return "llm.prepare_interim_filler"
}

// LLMVariableConfirmRequestEvent is fired by the context handler when an
// autoconfirm variable tool succeeds. The LLM handler intercepts it, bridges
// any already-spoken filler into the confirmation phrase via a small AI call,
// then fires a TTSSpeakEvent with the result. This produces a single natural
// utterance rather than two disconnected spoken fragments.
type LLMVariableConfirmRequestEvent struct {
	ConfirmText string // pre-generated confirmation phrase to deliver (or bridge into)
}

func (e *LLMVariableConfirmRequestEvent) GetId() string {
	return "llm.variable_confirm_request"
}
