package silero

import (
	"context"
	"fmt"

	"interactkit/core"
	"interactkit/utils/audio"
)

// Config holds configuration for the Silero VAD service
type Config struct {
	OnnxPath        string  `json:"onnx_path"`
	OnnxRuntimePath string  `json:"onnx_runtime_path"`
	Threshold       float32 `json:"threshold"`
}

// DefaultConfig returns a Config with sensible defaults
func DefaultConfig() Config {
	return Config{
		OnnxPath:        "./external/models/silero_vad.onnx",
		OnnxRuntimePath: "./external/onnx/libonnxruntime.so",
		Threshold:       0.3,
	}
}

type SileroVadService struct {
	vad      *SileroVAD
	denoiser *audio.RNNoiseDenoiser
	logger   *core.Logger
}

// NewSileroVadService creates a new Silero VAD service.
// Use DefaultConfig() to get a config with sensible defaults and override only what you need.
func NewSileroVadService(config Config, logger *core.Logger) (*SileroVadService, error) {
	vad, err := NewSileroVAD(SileroVADConfig{
		OnnxPath:    config.OnnxPath,
		OnnxLibPath: config.OnnxRuntimePath,
		Threshold:   config.Threshold,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Silero VAD: %w", err)
	}
	// Silero VAD requires 16 kHz mono; create the denoiser for that format so
	// the RNNoise model and resamplers persist across chunks (no per-call startup).
	denoiser, err := audio.NewRNNoiseDenoiser(16000, 1)
	if err != nil {
		return nil, fmt.Errorf("failed to create RNNoise denoiser: %w", err)
	}
	if logger == nil {
		logger = core.GetLogger()
	}
	return &SileroVadService{
		vad:      vad,
		denoiser: denoiser,
		logger:   logger,
	}, nil
}

func (s *SileroVadService) Initialize(ctx context.Context) error {
	return s.vad.Initialize()
}

func (s *SileroVadService) Name() string {
	return "SileroVAD"
}

func (s *SileroVadService) Cleanup() error {
	s.logger.Info("Cleaning up Silero VAD service")
	if s.denoiser != nil {
		s.denoiser.Close()
	}
	return s.vad.Close()
}

func (s *SileroVadService) Reset() error {
	return nil
}

func (s *SileroVadService) Close() {
	if s.denoiser != nil {
		s.denoiser.Close()
	}
	s.vad.Close()
}

func (s *SileroVadService) ProcessAudio(input core.AudioChunk) (core.VADResult, error) {
	converted, err := audio.ConvertAudioChunk(input, core.PCM, 1, 16000)
	if err != nil {
		return core.VADResult{}, fmt.Errorf("convert failed: %w", err)
	}
	denoised, desnoiseErr := s.denoiser.DenoiseChunk(converted)
	if desnoiseErr != nil {
		s.logger.Errorf("Denoising failed: %v", desnoiseErr)
		return core.VADResult{}, fmt.Errorf("denoise failed: %w", desnoiseErr)
	}
	// The denoiser buffers samples until a full 480-sample RNNoise frame is
	// available. On the very first call it may return empty audio; fall back to
	// the pre-denoised audio so VAD keeps running.
	vadInput := denoised.Data
	if len(*vadInput) == 0 {
		vadInput = converted.Data
	}
	conf, err := s.vad.VoiceConfidence(*vadInput, 16000)
	if err != nil {
		return core.VADResult{}, fmt.Errorf("VAD processing failed: %w", err)
	}
	return core.VADResult{
		Ready:      true,
		Confidence: conf,
	}, nil
}
