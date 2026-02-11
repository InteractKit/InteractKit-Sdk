package llm

import (
	"context"
	"fmt"
	"interactkit/core"
	"sync"

	"github.com/bytedance/sonic"
	"github.com/sashabaranov/go-openai"
)

// OpenAILLMService implements the LLMService interface using OpenAI
type OpenAILLMService struct {
	client      *openai.Client
	apiKey      string
	model       string
	maxTokens   int
	temperature float32
	streaming   bool

	// Streaming management
	activeStreams map[string]*openai.ChatCompletionStream
	streamsMutex  sync.RWMutex
	ctx           context.Context
	cancel        context.CancelFunc

	// Service state
	isInitialized bool
	mu            sync.RWMutex
}

// Config holds the configuration for OpenAI service
type Config struct {
	APIKey      string
	Model       string
	MaxTokens   int
	Temperature float32
	Streaming   bool
}

// NewOpenAILLMService creates a new instance of OpenAILLMService
func NewOpenAILLMService(config Config) *OpenAILLMService {
	return &OpenAILLMService{
		apiKey:        config.APIKey,
		model:         config.Model,
		maxTokens:     config.MaxTokens,
		temperature:   config.Temperature,
		streaming:     config.Streaming,
		activeStreams: make(map[string]*openai.ChatCompletionStream),
	}
}

// Init initializes the OpenAI service
func (s *OpenAILLMService) Init(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.apiKey == "" {
		return fmt.Errorf("OpenAI API key is required")
	}

	// Create context with cancel for managing streams
	s.ctx, s.cancel = context.WithCancel(context.Background())

	s.client = openai.NewClient(s.apiKey)

	// Test the connection
	_, err := s.client.ListModels(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to OpenAI: %w", err)
	}

	s.isInitialized = true
	return nil
}

// Cleanup performs cleanup operations
func (s *OpenAILLMService) Cleanup() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Stop all active streams
	s.stopAllStreams()

	// Cancel context
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}

	s.client = nil
	s.isInitialized = false

	return nil
}

// Reset resets the service state and stops all active streams
func (s *OpenAILLMService) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Stop all active streams immediately
	s.stopAllStreams()

	// Cancel existing context
	if s.cancel != nil {
		s.cancel()
	}

	// Create new context
	s.ctx, s.cancel = context.WithCancel(context.Background())

	// Recreate client with same config
	s.client = openai.NewClient(s.apiKey)

	// Clear active streams map
	s.activeStreams = make(map[string]*openai.ChatCompletionStream)

	return nil
}

// stopAllStreams stops all active streaming sessions
func (s *OpenAILLMService) stopAllStreams() {
	s.streamsMutex.Lock()
	defer s.streamsMutex.Unlock()

	for id, stream := range s.activeStreams {
		if stream != nil {
			stream.Close()
		}
		delete(s.activeStreams, id)
	}
}

// registerStream adds a stream to the active streams map
func (s *OpenAILLMService) registerStream(id string, stream *openai.ChatCompletionStream) {
	s.streamsMutex.Lock()
	defer s.streamsMutex.Unlock()
	s.activeStreams[id] = stream
}

// unregisterStream removes a stream from the active streams map
func (s *OpenAILLMService) unregisterStream(id string) {
	s.streamsMutex.Lock()
	defer s.streamsMutex.Unlock()
	delete(s.activeStreams, id)
}

// generateStreamID generates a unique ID for a stream
func (s *OpenAILLMService) generateStreamID() string {
	return fmt.Sprintf("%p-%d", s, len(s.activeStreams))
}

// RunCompletion runs a completion against OpenAI
func (s *OpenAILLMService) RunCompletion(
	context core.LLMContext,
	outChan chan<- string,
	toolInvocationChan chan<- core.LLMToolCall,
	FatalServiceErrorChan chan<- error,
	completionStartChan chan<- struct{},
	completionEndChan chan<- struct{},
) {
	// Check if service is initialized
	s.mu.RLock()
	if !s.isInitialized {
		s.mu.RUnlock()
		FatalServiceErrorChan <- fmt.Errorf("OpenAI service not initialized")
		return
	}
	s.mu.RUnlock()

	// Check if reset was called (context cancelled)
	select {
	case <-s.ctx.Done():
		FatalServiceErrorChan <- fmt.Errorf("service was reset during completion")
		return
	default:
	}

	// Signal that completion is starting
	select {
	case completionStartChan <- struct{}{}:
	default:
	}

	defer func() {
		select {
		case completionEndChan <- struct{}{}:
		default:
		}
	}()

	// Convert core messages to OpenAI messages
	openAIMessages, err := s.convertMessages(context.Messages)
	if err != nil {
		FatalServiceErrorChan <- fmt.Errorf("failed to convert messages: %w", err)
		return
	}

	// Create chat completion request
	req := openai.ChatCompletionRequest{
		Model:       s.model,
		Messages:    openAIMessages,
		MaxTokens:   s.maxTokens,
		Temperature: s.temperature,
		Stream:      s.streaming,
	}

	// Add tools if available
	if len(context.Tools) > 0 {
		tools, err := s.convertTools(context.Tools)
		if err != nil {
			FatalServiceErrorChan <- fmt.Errorf("failed to convert tools: %w", err)
			return
		}
		req.Tools = tools
	}

	if s.streaming {
		s.runStreamingCompletion(req, outChan, toolInvocationChan, FatalServiceErrorChan)
	} else {
		s.runNonStreamingCompletion(req, outChan, toolInvocationChan, FatalServiceErrorChan)
	}
}

// runStreamingCompletion handles streaming responses
func (s *OpenAILLMService) runStreamingCompletion(
	req openai.ChatCompletionRequest,
	outChan chan<- string,
	toolInvocationChan chan<- core.LLMToolCall,
	FatalServiceErrorChan chan<- error,
) {
	// Check if service was reset
	select {
	case <-s.ctx.Done():
		FatalServiceErrorChan <- fmt.Errorf("service was reset during streaming")
		return
	default:
	}

	stream, err := s.client.CreateChatCompletionStream(s.ctx, req)
	if err != nil {
		FatalServiceErrorChan <- fmt.Errorf("failed to create completion stream: %w", err)
		return
	}

	// Generate stream ID and register it
	streamID := s.generateStreamID()
	s.registerStream(streamID, stream)
	defer func() {
		s.unregisterStream(streamID)
		stream.Close()
	}()

	var toolCallBuilder = make(map[int]*openai.ToolCall)

	for {
		select {
		case <-s.ctx.Done():
			// Service was reset, stop streaming immediately
			return
		default:
		}

		response, err := stream.Recv()
		if err != nil {
			break
		}

		if len(response.Choices) > 0 {
			choice := response.Choices[0]

			// Handle content streaming
			if choice.Delta.Content != "" {
				select {
				case <-s.ctx.Done():
					return
				case outChan <- choice.Delta.Content:
				}
			}

			// Handle tool calls
			if len(choice.Delta.ToolCalls) > 0 {
				for _, toolCall := range choice.Delta.ToolCalls {
					// Handle incremental tool calls (OpenAI streams tool calls in chunks)
					if toolCall.Index != nil {
						idx := *toolCall.Index

						// Initialize or update tool call at this index
						if _, exists := toolCallBuilder[idx]; !exists {
							toolCallBuilder[idx] = &openai.ToolCall{
								Index:    toolCall.Index,
								ID:       toolCall.ID,
								Type:     toolCall.Type,
								Function: openai.FunctionCall{},
							}
						}

						// Accumulate function name and arguments
						if toolCall.Function.Name != "" {
							toolCallBuilder[idx].Function.Name = toolCall.Function.Name
						}
						if toolCall.Function.Arguments != "" {
							toolCallBuilder[idx].Function.Arguments += toolCall.Function.Arguments
						}
						if toolCall.ID != "" {
							toolCallBuilder[idx].ID = toolCall.ID
						}
					}
				}
			}

			// Check for finish reason to send complete tool calls
			if choice.FinishReason == "tool_calls" {
				for _, toolCall := range toolCallBuilder {
					if toolCall.Function.Name != "" {
						select {
						case <-s.ctx.Done():
							return
						case toolInvocationChan <- s.convertToolCall(*toolCall):
						}
					}
				}
				// Clear builder
				toolCallBuilder = make(map[int]*openai.ToolCall)
			}
		}
	}
}

// runNonStreamingCompletion handles non-streaming responses
func (s *OpenAILLMService) runNonStreamingCompletion(
	req openai.ChatCompletionRequest,
	outChan chan<- string,
	toolInvocationChan chan<- core.LLMToolCall,
	FatalServiceErrorChan chan<- error,
) {
	// Check if service was reset
	select {
	case <-s.ctx.Done():
		FatalServiceErrorChan <- fmt.Errorf("service was reset during completion")
		return
	default:
	}

	resp, err := s.client.CreateChatCompletion(s.ctx, req)
	if err != nil {
		FatalServiceErrorChan <- fmt.Errorf("failed to create completion: %w", err)
		return
	}

	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]

		// Send content
		if choice.Message.Content != "" {
			select {
			case <-s.ctx.Done():
				return
			case outChan <- choice.Message.Content:
			}
		}

		// Handle tool calls
		if len(choice.Message.ToolCalls) > 0 {
			for _, toolCall := range choice.Message.ToolCalls {
				select {
				case <-s.ctx.Done():
					return
				case toolInvocationChan <- s.convertToolCall(toolCall):
				}
			}
		}
	}
}

// convertMessages converts core messages to OpenAI messages
func (s *OpenAILLMService) convertMessages(messages []core.LLMMessage) ([]openai.ChatCompletionMessage, error) {
	openAIMessages := make([]openai.ChatCompletionMessage, 0, len(messages))

	for _, msg := range messages {
		openAIMsg := openai.ChatCompletionMessage{
			Role:    s.convertRole(msg.Role),
			Content: msg.Message,
		}

		// Handle media content
		if msg.Media != nil && len(*msg.Media) > 0 {
			content := []openai.ChatMessagePart{
				{
					Type: openai.ChatMessagePartTypeText,
					Text: msg.Message,
				},
			}

			for _, media := range *msg.Media {
				mediaURL, err := s.convertMediaToURL(media)
				if err != nil {
					return nil, err
				}

				content = append(content, openai.ChatMessagePart{
					Type: openai.ChatMessagePartTypeImageURL,
					ImageURL: &openai.ChatMessageImageURL{
						URL: mediaURL,
					},
				})
			}

			openAIMsg.MultiContent = content
			openAIMsg.Content = "" // Clear content when using multi-content
		}

		openAIMessages = append(openAIMessages, openAIMsg)
	}

	return openAIMessages, nil
}

// convertTools converts core tools to OpenAI tools
func (s *OpenAILLMService) convertTools(tools []core.LLMTool) ([]openai.Tool, error) {
	openAITools := make([]openai.Tool, 0, len(tools))

	for _, tool := range tools {
		parameters := make(map[string]interface{})
		properties := make(map[string]interface{})
		required := make([]string, 0)

		for _, param := range tool.Parameters {
			prop := map[string]interface{}{
				"type":        s.convertParameterType(param.Type),
				"description": param.Description,
			}

			if param.Example != "" {
				prop["example"] = param.Example
			}

			properties[param.Name] = prop

			if param.Required {
				required = append(required, param.Name)
			}
		}

		parameters["type"] = "object"
		parameters["properties"] = properties
		if len(required) > 0 {
			parameters["required"] = required
		}

		paramsJSON, err := sonic.Marshal(parameters)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal parameters: %w", err)
		}

		openAITools = append(openAITools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        tool.ToolId,
				Description: tool.Description,
				Parameters:  paramsJSON,
			},
		})
	}

	return openAITools, nil
}

// convertRole converts core role to OpenAI role
func (s *OpenAILLMService) convertRole(role core.LLMMessageRole) string {
	switch role {
	case core.LLMMessageRoleUser:
		return openai.ChatMessageRoleUser
	case core.LLMMessageRoleAssistant:
		return openai.ChatMessageRoleAssistant
	case core.LLMMessageRoleSystem:
		return openai.ChatMessageRoleSystem
	case core.LLMMessageRoleTool:
		return openai.ChatMessageRoleTool
	default:
		return openai.ChatMessageRoleUser
	}
}

// convertParameterType converts core parameter type to JSON schema type
func (s *OpenAILLMService) convertParameterType(paramType core.LLMParamterType) string {
	switch paramType {
	case core.LLMParameterTypeString:
		return "string"
	case core.LLMParameterTypeInteger:
		return "integer"
	case core.LLMParameterTypeBoolean:
		return "boolean"
	case core.LLMParameterTypeObject:
		return "object"
	default:
		return "string"
	}
}

// convertToolCall converts OpenAI tool call to core tool call
func (s *OpenAILLMService) convertToolCall(toolCall openai.ToolCall) core.LLMToolCall {
	var parameters map[string]interface{}

	if toolCall.Function.Arguments != "" {
		err := sonic.Unmarshal([]byte(toolCall.Function.Arguments), &parameters)
		if err != nil {
			// If unmarshaling fails, create a map with the raw arguments
			parameters = map[string]interface{}{
				"raw_arguments": toolCall.Function.Arguments,
			}
		}
	}

	return core.LLMToolCall{
		ToolId:     toolCall.Function.Name,
		Parameters: &parameters,
	}
}

// convertMediaToURL converts media to a data URL for OpenAI
func (s *OpenAILLMService) convertMediaToURL(media core.LLMMedia) (string, error) {
	mediaType := s.convertMediaType(media.MediaType)
	return fmt.Sprintf("data:%s;base64,%s", mediaType, media.Data), nil
}

// convertMediaType converts core media type to MIME type
func (s *OpenAILLMService) convertMediaType(mediaType core.LLMMediaType) string {
	switch mediaType {
	case core.LLMMediaTypeImagePNG:
		return "image/png"
	case core.LLMMediaTypeImageJPEG:
		return "image/jpeg"
	case core.LLMMediaTypeAudioMP3:
		return "audio/mpeg"
	case core.LLMMediaTypeAudioWAV:
		return "audio/wav"
	case core.LLMMediaTypeAudioPCM:
		return "audio/pcm"
	case core.LLMMediaTypeVideoMP4:
		return "video/mp4"
	case core.LLMMediaTypeVideoWebM:
		return "video/webm"
	default:
		return "application/octet-stream"
	}
}
