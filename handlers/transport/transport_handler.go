package transport

import (
	"context"
	"interactkit/core"
	"interactkit/events/transport"
	"interactkit/utils/audio"
)

type TransportService interface {
	core.IService
	Connect() error
	SendRawOutput(data core.RawData) error
	StartReceiving(outputChan chan<- core.RawData, errorChan chan<- error)
}

// TransportHandlerWrapper holds shared state
type TransportHandlerWrapper struct {
	service        TransportService
	backupServices []TransportService
	ctx            context.Context
	config         TransportConfig

	// Shared state
	connected bool
}

func NewTransportHandlerWrapper(
	service TransportService,
	backupServices []TransportService,
	ctx context.Context,
	config TransportConfig,
) *TransportHandlerWrapper {
	return &TransportHandlerWrapper{
		service:        service,
		backupServices: backupServices,
		ctx:            ctx,
		config:         config,
	}
}

func (w *TransportHandlerWrapper) GetInputHandler() *TransportInputHandler {
	typedServices := make([]core.IService, len(w.backupServices))
	for i, s := range w.backupServices {
		typedServices[i] = s
	}

	return &TransportInputHandler{
		BaseHandler: *core.NewBaseHandler(
			w.service, typedServices, w.ctx,
		),
		config:  w.config,
		wrapper: w,
	}
}

func (w *TransportHandlerWrapper) GetOutputHandler() *TransportOutputHandler {
	typedServices := make([]core.IService, len(w.backupServices))
	for i, s := range w.backupServices {
		typedServices[i] = s
	}

	return &TransportOutputHandler{
		BaseHandler: *core.NewBaseHandler(
			w.service, typedServices, w.ctx,
		),
		config:  w.config,
		wrapper: w,
	}
}

// TransportInputHandler handles incoming data
type TransportInputHandler struct {
	core.BaseHandler
	config  TransportConfig
	wrapper *TransportHandlerWrapper
}

func (h *TransportInputHandler) Init(
	inputChan <-chan *core.EventPacket,
	outputNextChan chan<- *core.EventPacket,
	outputTopChan chan<- *core.EventPacket,
	ctx context.Context,
) error {
	err := h.BaseHandler.Initialize(inputChan, outputNextChan, outputTopChan, ctx)
	if err != nil {
		return err
	}

	if !h.wrapper.connected {
		err = h.Service.(TransportService).Connect()
		if err != nil {
			return err
		}
		h.wrapper.connected = true
	}
	return nil
}

func (h *TransportInputHandler) Start() error {
	outputChan := make(chan core.RawData)
	errorChan := make(chan error)

	go h.Service.(TransportService).StartReceiving(outputChan, errorChan)

	for {
		select {
		case outputData := <-outputChan:
			audioChunk, videoChunk, text, err := h.config.serializer.Deserialize(outputData)
			if err != nil {
				h.HandlerErrorChan <- err
				continue
			}

			if audioChunk.Data != nil {
				audioEvent := &transport.TransportAudioInputEvent{
					AudioChunk: audioChunk,
				}
				h.SendPacket(core.NewEventPacket(audioEvent, core.EventRelayDestinationNextService, "TransportInputHandler"))
			}

			if videoChunk.Data != nil {
				videoEvent := &transport.TransportVideoInputEvent{
					VideoChunk: videoChunk,
				}
				h.SendPacket(core.NewEventPacket(videoEvent, core.EventRelayDestinationNextService, "TransportInputHandler"))
			}

			if text != "" {
				textEvent := &transport.TransportTextInputEvent{
					Text: text,
				}
				h.SendPacket(core.NewEventPacket(textEvent, core.EventRelayDestinationNextService, "TransportInputHandler"))
			}

		case err := <-errorChan:
			h.FatalServiceErrorChan <- err
		case <-h.Ctx.Done():
			return nil
		}
	}
}

func (h *TransportInputHandler) HandleEvent(eventPacket *core.EventPacket) error {
	h.SendPacket(eventPacket)
	return nil
}

// TransportOutputHandler handles outgoing data
type TransportOutputHandler struct {
	core.BaseHandler
	config  TransportConfig
	wrapper *TransportHandlerWrapper
}

func (h *TransportOutputHandler) Init(
	inputChan <-chan *core.EventPacket,
	outputNextChan chan<- *core.EventPacket,
	outputTopChan chan<- *core.EventPacket,
	ctx context.Context,
) error {
	err := h.BaseHandler.Initialize(inputChan, outputNextChan, outputTopChan, ctx)
	if err != nil {
		return err
	}

	if !h.wrapper.connected {
		err = h.Service.(TransportService).Connect()
		if err != nil {
			return err
		}
		h.wrapper.connected = true
	}
	return nil
}

func (h *TransportOutputHandler) Start() error {
	<-h.Ctx.Done()
	return nil
}

func (h *TransportOutputHandler) HandleEvent(eventPacket *core.EventPacket) error {
	switch event := eventPacket.Event.(type) {
	case *transport.TransportAudioOutputEvent:
		processedAudioChunk, err := audio.ConvertAudioChunk(
			event.AudioChunk, h.config.OutAudioFormat, h.config.OutChannels, h.config.OutSampleRate,
		)
		if err != nil {
			h.HandlerErrorChan <- err
			return err
		}
		event.AudioChunk = processedAudioChunk
		rawData, err := h.config.serializer.SerializeAudioOutput(event.AudioChunk)
		if err != nil {
			h.HandlerErrorChan <- err
			return err
		}
		err = h.Service.(TransportService).SendRawOutput(rawData)
		if err != nil {
			h.FatalServiceErrorChan <- err
			return err
		}

	case *transport.TransportVideoOutputEvent:
		rawData, err := h.config.serializer.SerializeVideoOutput(event.VideoChunk)
		if err != nil {
			h.FatalServiceErrorChan <- err
			return err
		}
		err = h.Service.(TransportService).SendRawOutput(rawData)
		if err != nil {
			h.FatalServiceErrorChan <- err
			return err
		}

	case *transport.TransportTextOutputEvent:
		rawData, err := h.config.serializer.SerializeTextOutput(event.Text)
		if err != nil {
			h.FatalServiceErrorChan <- err
			return err
		}
		err = h.Service.(TransportService).SendRawOutput(rawData)
		if err != nil {
			h.FatalServiceErrorChan <- err
			return err
		}
	}

	h.SendPacket(eventPacket)
	return nil
}
