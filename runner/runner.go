package runner

import (
	"context"
	"interactkit/core"
)

type Runner struct {
	Handlers       []core.IHandler
	ctx            context.Context
	cancel         context.CancelFunc
	topOutputChan  chan *core.EventPacket
	lastOutputChan chan *core.EventPacket
}

func NewRunner(handlers []core.IHandler) *Runner {
	return &Runner{
		Handlers: handlers,
	}
}

func (r *Runner) Start() error {
	if len(r.Handlers) == 0 {
		return nil
	}

	r.ctx, r.cancel = context.WithCancel(context.Background())
	r.topOutputChan = make(chan *core.EventPacket, 100)
	r.lastOutputChan = make(chan *core.EventPacket, 100)

	// Create channels for each handler's input
	inputChans := make([]chan *core.EventPacket, len(r.Handlers))
	for i := range inputChans {
		inputChans[i] = make(chan *core.EventPacket, 100)
	}

	// Initialize handlers with proper channel connections
	for i, handler := range r.Handlers {
		var outputNextChan chan<- *core.EventPacket

		if i < len(r.Handlers)-1 {
			// Not the last handler - output goes to next handler's input
			outputNextChan = inputChans[i+1]
		} else {
			// Last handler - output goes to our capture channel
			outputNextChan = r.lastOutputChan
		}

		err := handler.Initialize(
			inputChans[i],
			outputNextChan,
			r.topOutputChan,
			r.ctx,
		)
		if err != nil {
			r.cancel()
			return err
		}

		if err := handler.Start(); err != nil {
			r.cancel()
			return err
		}
	}

	// Start listeners for output channels
	go r.listenToOutputs()

	return nil
}

func (r *Runner) listenToOutputs() {
	for {
		select {
		case packet := <-r.lastOutputChan:
			// Process final output from the last handler
			r.processFinalOutput(packet)
		case packet := <-r.topOutputChan:
			// Process events destined for the top service
			r.processTopOutput(packet)
		case <-r.ctx.Done():
			return
		}
	}
}

func (r *Runner) processFinalOutput(packet *core.EventPacket) {
	// Handle the final output from the chain
	// This could be logging, storing in database, forwarding to external system, etc.
	// fmt.Printf("Final output: %+v\n", packet)
}

func (r *Runner) processTopOutput(packet *core.EventPacket) {
	// Handle events that need to go to the top service
	// if error occurs during processing, we can log or handle it accordingly else echo back to the first handler

	switch packet.Event.(type) {
	case *core.CriticalErrorEvent:
		// log the error
	default:
		// echo back to first handler
		r.Handlers[0].HandleEvent(packet)
	}
}

func (r *Runner) Stop() error {
	if r.cancel != nil {
		r.cancel()
	}

	var errs []error
	for _, handler := range r.Handlers {
		if err := handler.Cleanup(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

func (r *Runner) Reset() error {
	var errs []error
	for _, handler := range r.Handlers {
		if err := handler.Reset(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}
