package llm

import (
	"context"
	"interactkit/core"
	"interactkit/events/llm"
)

type LLMService interface {
	core.IService
	RunCompletion(
		context core.LLMContext,
		outChan chan<- string,
		toolInvocationChan chan<- core.LLMToolCall,
		FatalServiceErrorChan chan<- error,
		completionStartChan chan<- struct{},
		completionEndChan chan<- struct{},
	)
}

type LLMHandler struct {
	core.BaseHandler
	messageOutChan        chan string
	toolInvocationOutChan chan core.LLMToolCall
	completionStartChan   chan struct{}
	completionEndChan     chan struct{}
	config                LLMHandlerConfig
}

func NewLLMHandler(service LLMService, backupServices []LLMService, ctx context.Context, config LLMHandlerConfig) *LLMHandler {
	typedServices := make([]core.IService, len(backupServices))
	for i, s := range backupServices {
		typedServices[i] = s
	}
	return &LLMHandler{
		BaseHandler: *core.NewBaseHandler(
			service, typedServices, ctx,
		),
		config: config,
	}
}

func (h *LLMHandler) Init(
	inputChan <-chan *core.EventPacket,
	outputNextChan chan<- *core.EventPacket,
	outputTopChan chan<- *core.EventPacket,
	ctx context.Context,
) error {
	h.messageOutChan = make(chan string)
	h.toolInvocationOutChan = make(chan core.LLMToolCall)
	h.completionStartChan = make(chan struct{})
	h.completionEndChan = make(chan struct{})
	return h.BaseHandler.Initialize(inputChan, outputNextChan, outputTopChan, ctx)
}

func (h *LLMHandler) Start() error {
	// Create a goroutine to listen for incoming events and process them.
	go h.eventLoop()

	// Start the main event loop for the handler. This will listen for incoming events and process them accordingly.
	for {
		select {
		case event := <-h.InputChan:
			err := h.HandleEvent(event)
			if err != nil {
				h.HandleError(err)
			}
		case <-h.Ctx.Done():
			return nil

		}
	}
}

func (h *LLMHandler) eventLoop() {
	var fullText string
	// listen messages on unique channels for LLM messages, tool invocations, and fatal errors. This allows the handler to process these events and relay them to the appropriate destinations.
	for {
		select {
		case msg := <-h.messageOutChan:
			// Relay the LLM message to the next service in the pipeline.
			h.SendPacket(core.NewEventPacket(&llm.LLMResponseChunkEvent{
				Chunk: msg,
			}, core.EventRelayDestinationNextService, "LLMHandler"))
			fullText += msg
		case toolCall := <-h.toolInvocationOutChan:
			// Relay the tool invocation request to the next service in the pipeline.
			h.SendPacket(core.NewEventPacket(&llm.LLMToolInvocationRequestedEvent{
				ToolId: toolCall.ToolId,
				Params: toolCall.Parameters,
			}, core.EventRelayDestinationNextService, "LLMHandler"))
		case <-h.completionStartChan:
			h.SendPacket(
				core.NewEventPacket(
					&llm.LLMResponseStartedEvent{},
					core.EventRelayDestinationNextService,
					"LLMHandler",
				),
			)
			fullText = "" // Reset aggregation at start
		case <-h.completionEndChan:
			h.SendPacket(
				core.NewEventPacket(
					&llm.LLMResponseCompletedEvent{
						FullText: fullText,
					},
					core.EventRelayDestinationNextService,
					"LLMHandler",
				),
			)
		case <-h.Ctx.Done():
			return
		}
	}
}

func (h *LLMHandler) HandleEvent(packet *core.EventPacket) error {
	switch e := packet.Event.(type) {
	case *llm.LLMGenerateResponseEvent:
		go h.Service.(LLMService).RunCompletion(
			e.Context,
			h.messageOutChan,
			h.toolInvocationOutChan,
			h.FatalServiceErrorChan,
			h.completionStartChan,
			h.completionEndChan,
		)
	default:
	}
	h.SendPacket(packet)
	return nil
}

func (h *LLMHandler) Cleanup() error {
	err := h.BaseHandler.Cleanup()
	close(h.messageOutChan)
	close(h.toolInvocationOutChan)
	close(h.completionStartChan)
	close(h.completionEndChan)
	return err
}
