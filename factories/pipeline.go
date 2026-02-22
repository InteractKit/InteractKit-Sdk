package factories

import (
	"context"
	"interactkit/core"
	"interactkit/handlers/transport"
	"runtime"
	"runtime/debug"
	"time"
)

// PipelineConfig configures a Pipeline's lifecycle behaviour.
type PipelineConfig struct {
	Timeout time.Duration
}

// HandlerBuilder creates the ordered handler slice for a single job.
// It receives the transport service and the job context.
type HandlerBuilder func(svc transport.ITransportService, ctx context.Context) ([]core.IHandler, error)

// Pipeline builds and runs handler pipelines for incoming transport jobs.
type Pipeline struct {
	config  PipelineConfig
	builder HandlerBuilder
	logger  *core.Logger
}

// NewPipeline creates a Pipeline that uses builder to construct handlers per-job.
func NewPipeline(builder HandlerBuilder, config PipelineConfig, logger *core.Logger) *Pipeline {
	if logger == nil {
		logger = core.GetLogger()
	}
	return &Pipeline{
		builder: builder,
		config:  config,
		logger:  logger,
	}
}

// Run builds a handler pipeline for a single job and blocks until completion.
func (p *Pipeline) Run(svc transport.ITransportService, ctx context.Context) error {
	// Use per-session logger if available, otherwise fall back to pipeline logger.
	base := core.SessionLoggerFromContext(ctx)
	if base == nil {
		base = p.logger
	}
	logger := base.With(map[string]any{"component": "pipeline"})

	select {
	case <-ctx.Done():
		logger.Info("context already cancelled, skipping job")
		return nil
	default:
	}

	if svc == nil {
		logger.Warn("nil transport service, skipping job")
		return nil
	}

	handlers, err := p.builder(svc, ctx)
	if err != nil {
		logger.With(map[string]any{"error": err}).Error("failed to build handlers")
		return err
	}

	runner := core.NewRunner(handlers, base)
	if err := runner.Start(); err != nil {
		logger.With(map[string]any{"error": err}).Error("runner failed to start")
		return err
	}

	logger.Info("runner started, waiting for completion")

	var timerC <-chan time.Time
	if p.config.Timeout > 0 {
		timer := time.NewTimer(p.config.Timeout)
		defer timer.Stop()
		timerC = timer.C
	}

	var result error
	select {
	case <-ctx.Done():
		logger.Info("context cancelled, stopping runner")
		runner.Stop()

	case <-timerC:
		logger.Warn("timeout reached, stopping runner")
		runner.Stop()
		result = context.DeadlineExceeded

	case <-runner.Finished:
		logger.Info("runner finished")
	}

	// Force GC and release memory back to the OS after each session.
	// Per-session C allocations (ONNX tensors, Opus codecs, RNNoise,
	// libsamplerate) are invisible to Go's GC so it doesn't know to
	// collect aggressively. Without this, RSS climbs ~10MB per session.
	runtime.GC()
	debug.FreeOSMemory()
	logger.Info("post-session GC completed")

	return result
}

// Serve registers a job handler with the provider, starts it,
// and blocks until ctx is cancelled. It then stops the provider.
func (p *Pipeline) Serve(provider transport.ITransportProvider, ctx context.Context) error {
	logger := p.logger.With(map[string]any{"component": "pipeline"})

	if err := provider.RegisterJobHandler(func(svc transport.ITransportService, jobCtx context.Context) error {
		return p.Run(svc, jobCtx)
	}); err != nil {
		logger.With(map[string]any{"error": err}).Error("failed to register job handler")
		return err
	}

	go func() {
		if err := provider.Start(); err != nil {
			logger.With(map[string]any{"error": err}).Error("provider failed to start")
		}
	}()

	logger.Info("provider started, waiting for jobs")
	<-ctx.Done()

	logger.Info("stopping provider")
	if err := provider.Stop(); err != nil {
		logger.With(map[string]any{"error": err}).Error("error stopping provider")
	}

	return nil
}
