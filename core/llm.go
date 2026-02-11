package core

type LLMMessageRole string

const (
	LLMMessageRoleUser      LLMMessageRole = "user"
	LLMMessageRoleAssistant LLMMessageRole = "assistant"
	LLMMessageRoleSystem    LLMMessageRole = "system"
	LLMMessageRoleTool      LLMMessageRole = "tool"
)

type LLMMediaType string

const (
	LLMMediaTypeAudioPCM  LLMMediaType = "audio/pcm"
	LLMMediaTypeAudioWAV  LLMMediaType = "audio/wav"
	LLMMediaTypeAudioMP3  LLMMediaType = "audio/mpeg"
	LLMMediaTypeVideoMP4  LLMMediaType = "video/mp4"
	LLMMediaTypeVideoWebM LLMMediaType = "video/webm"
	LLMMediaTypeImagePNG  LLMMediaType = "image/png"
	LLMMediaTypeImageJPEG LLMMediaType = "image/jpeg"
)

type LLMMedia struct {
	Data      []byte       // Raw media data.
	MediaType LLMMediaType // Type of the media (e.g., "audio/wav", "video/mp4").
}

// LLMMessage represents a message exchanged with the LLM.
type LLMMessage struct {
	Role    LLMMessageRole `json:"role"`            // Role of the message sender (e.g., user, assistant, system , tool).
	Message string         `json:"message"`         // Content of the message.
	Media   *[]LLMMedia    `json:"media,omitempty"` // Optional media content associated with the message.
}

type LLMParamterType string

const (
	LLMParameterTypeString  LLMParamterType = "string"
	LLMParameterTypeInteger LLMParamterType = "number"
	LLMParameterTypeBoolean LLMParamterType = "boolean"
	LLMParameterTypeObject  LLMParamterType = "object"
)

// Parameter represents a parameter for an LLM tool.
type Parameter struct {
	Name        string          `json:"name"`        // Name of the parameter.
	Description string          `json:"description"` // Description of the parameter.
	Required    bool            `json:"required"`    // Whether the parameter is required.
	Example     string          `json:"example"`     // Example value for the parameter.
	Type        LLMParamterType `json:"type"`        // Type of the parameter (e.g., string, integer).
}

// LLMTool represents a tool that can be used by the LLM.
type LLMTool struct {
	Name        string      `json:"name"`                 // Name of the tool.
	ToolId      string      `json:"tool_id"`              // Id of the tool.
	Description string      `json:"description"`          // Description of the tool's functionality.
	Parameters  []Parameter `json:"parameters,omitempty"` // Parameters required by the tool.
}

type LLMContext struct {
	Messages []LLMMessage
	Tools    []LLMTool
}

func (c *LLMContext) AddUserMessage(text string) {
	c.Messages = append(c.Messages, LLMMessage{Role: "user", Message: text})
}

func (c *LLMContext) AddAssistantMessage(text string) {
	c.Messages = append(c.Messages, LLMMessage{Role: "assistant", Message: text})
}

func (c *LLMContext) AddAssistantMessageChunk(chunk string) {
	// Append chunk to last assistant message or create new
}

func (c *LLMContext) SetAssistantMessage(text string) {
	// Set/update the last assistant message
}

func (c *LLMContext) GetLastAssistantMessage() string {
	// Return last assistant message
	return ""
}

// LLMToolCall represents a call to an LLM tool.
type LLMToolCall struct {
	ToolId     string          `json:"tool_id"`              // Id of the tool being called.
	Parameters *map[string]any `json:"parameters,omitempty"` // Parameters for the tool call.
}
