package core

import (
	"context"
	"sync"
)

type Runner struct {
	Handlers             []IHandler
	ExternalEventHandler *ExternalEventHandler
	Logger               *Logger
	ctx                  context.Context
	cancel               context.CancelFunc
	topOutputChan        chan *EventPacket
	lastOutputChan       chan *EventPacket
	Started              bool
	inputChans           []chan *EventPacket // store handler input channels for cleanup
	Finished             chan struct{}       // channel to signal when runner has finished processing
	startedReadMutex     sync.Mutex
}

func NewRunner(handlers []IHandler, logger *Logger) *Runner {
	if logger == nil {
		logger = GetLogger()
	}
	return &Runner{
		Handlers:             handlers,
		ExternalEventHandler: NewExternalEventHandler(logger),
		Logger:               logger,
		startedReadMutex:     sync.Mutex{},
	}
}

func (r *Runner) Start() error {
	r.Finished = make(chan struct{}, 5) // buffered to prevent blocking on send

	if len(r.Handlers) == 0 {
		return nil
	}

	r.startedReadMutex.Lock()
	r.Started = true
	r.startedReadMutex.Unlock()
	r.ctx, r.cancel = context.WithCancel(context.Background())
	r.topOutputChan = make(chan *EventPacket)
	r.lastOutputChan = make(chan *EventPacket)

	// Create channels for each handler's input
	inputChans := make([]chan *EventPacket, len(r.Handlers))
	for i := range inputChans {
		inputChans[i] = make(chan *EventPacket)
	}
	r.inputChans = inputChans // store refs for cleanup

	// Initialize handlers with proper channel connections
	for i, handler := range r.Handlers {
		var outputNextChan chan<- *EventPacket

		if i < len(r.Handlers)-1 {
			// Not the last handler - output goes to next handler's input
			outputNextChan = inputChans[i+1]
		} else {
			// Last handler - output goes to our capture channel
			outputNextChan = r.lastOutputChan
		}
		r.Logger.Infof("Initializing handler %d with outputNextChan connected to %T\n", i, outputNextChan)
		err := handler.Initialize(
			inputChans[i],
			outputNextChan,
			r.topOutputChan,
			r.ctx,
		)
		if err != nil {
			r.cancel()
			r.Finished <- struct{}{} // signal that runner has finished due to error
			return err
		}

	}

	// Start the ExternalEventHandler sidecar.
	r.ExternalEventHandler.Initialize(r.topOutputChan, r.ctx)

	// Start all handlers in separate goroutines
	for _, handler := range r.Handlers {
		go func(h IHandler) {
			if err := h.Start(); err != nil {
				r.Logger.Errorf("Error starting handler: %v", err)
				r.cancel()
			}
		}(handler)
	}
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

func (r *Runner) processFinalOutput(packet *EventPacket) {
	if event, ok := packet.Event.(*EndCallEvent); ok {
		r.Logger.Infof("EndCallEvent reached last node (reason: %q) — stopping runner", event.Reason)
		go r.Stop()
	}
}

func (r *Runner) processTopOutput(packet *EventPacket) {
	if _, ok := packet.Event.(IExternalOutputEvent); ok {
		r.ExternalEventHandler.Broadcast(packet)
	}

	switch event := packet.Event.(type) {
	case *CriticalErrorEvent:
		r.Logger.Errorf("Critical error received: %v", packet.Event)
		go r.Stop()

	case *EndCallEvent:
		r.Logger.Infof("EndCallEvent received (reason: %q) — relaying through pipeline", event.Reason)
		packet.Destination = EventRelayDestinationNextService
		go r.Handlers[0].HandleEvent(packet)

	default:
		packet.Destination = EventRelayDestinationNextService
		go r.Handlers[0].HandleEvent(packet)
	}
}

func (r *Runner) Stop() error {
	// Check if already stopped - make Stop idempotent
	r.startedReadMutex.Lock()
	if !r.Started {
		r.startedReadMutex.Unlock()
		r.Logger.Info("Runner already stopped, skipping")
		return nil
	}
	r.Started = false
	r.startedReadMutex.Unlock()

	defer func() {
		// recover from any panic during channel close
		if rec := recover(); rec != nil {
			r.Logger.Errorf("Recovered in Stop from: %v", rec)
		}
	}()

	defer func() {
		// Signal that runner has finished processing (non-blocking)
		select {
		case r.Finished <- struct{}{}:
		default:
		}
	}()

	if r.cancel != nil {
		r.cancel()
	}

	var errs []error
	for _, handler := range r.Handlers {
		r.Logger.Infof("Cleaning up handler: %T", handler)
		if err := handler.Cleanup(); err != nil {
			r.Logger.Errorf("Error cleaning up handler: %v", err)
			errs = append(errs, err)
		}
		r.Logger.Infof("Finished cleaning up handler: %T", handler)
	}

	r.Logger.Info("Runner stopped successfully, cleaning up channels")
	safeClose := func(ch chan *EventPacket) {
		defer func() {
			if rec := recover(); rec != nil {
			}
		}()
		close(ch)
	}
	safeClose(r.topOutputChan)
	safeClose(r.lastOutputChan)
	for _, ch := range r.inputChans {
		safeClose(ch)
	}
	r.Logger.Info("All channels closed successfully")

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
