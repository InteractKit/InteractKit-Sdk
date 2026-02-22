package silero

import (
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	ort "github.com/yalue/onnxruntime_go"
)

// onnxEnvOnce ensures the ONNX runtime environment is initialized exactly once
// for the entire process lifetime. Repeated Init/Destroy cycles leak ONNX
// internal state because the runtime is not designed to be torn down and
// re-created.
var onnxEnvOnce sync.Once

const (
	// MODEL_RESET_STATES_TIME is how often we reset internal model state (in seconds)
	MODEL_RESET_STATES_TIME = 2
)

// SileroVADConfig holds configuration for the Silero VAD
type SileroVADConfig struct {
	OnnxPath    string  // Path to the Silero ONNX model
	OnnxLibPath string  // Path to the ONNX runtime library
	Threshold   float32 // VAD threshold (0.0 - 1.0)
}

// DefaultSileroVADConfig returns a default configuration
func DefaultSileroVADConfig() SileroVADConfig {
	return SileroVADConfig{
		OnnxPath:    "./models/silero_vad.onnx",
		OnnxLibPath: "./onnx/libonnxruntime.so",
		Threshold:   0.3,
	}
}

// SileroVAD is a Voice Activity Detector using the Silero model
type SileroVAD struct {
	mu sync.Mutex

	config SileroVADConfig

	// ONNX session and tensors - created once and reused
	session      *ort.AdvancedSession
	inputTensor  *ort.Tensor[float32] // Reused for every inference
	srTensor     *ort.Tensor[int64]   // Reused (sample rate is constant)
	stateTensor  *ort.Tensor[float32] // Reused, content updated each time
	outputTensor *ort.Tensor[float32] // Reused, new output each inference
	stateNTensor *ort.Tensor[float32] // Reused, new state each inference

	// Internal state
	state          []float32 // Hidden state [2, 1, 128] - content updated, memory reused
	context        []float32 // Context buffer - content updated, memory reused
	audioBuffer    []float32 // Buffer for collecting incomplete audio samples
	lastResetTime  time.Time // Time when model state was last reset
	lastSampleRate int64     // Last used sample rate

	// Preallocated scratch buffers (single-threaded optimization)
	fullInput     []float32 // Preallocated buffer for context + input
	conversionBuf []float32 // Preallocated buffer for byte to float32 conversion
	sampleBuf     []int16   // Preallocated buffer for int16 samples during conversion

	initialized bool
}

// NewSileroVAD creates a new Silero VAD instance
func NewSileroVAD(config SileroVADConfig) (*SileroVAD, error) {
	vad := &SileroVAD{
		config:        config,
		state:         make([]float32, 2*1*128), // [2, 1, 128] - allocated once
		lastResetTime: time.Now(),
	}

	return vad, nil
}

// getInputLength returns the input length based on sample rate
func getInputLength(sampleRate int64) (int64, error) {
	switch sampleRate {
	case 8000:
		return 256, nil
	case 16000:
		return 512, nil
	default:
		return 0, fmt.Errorf("unsupported sample rate: %d (must be 8000 or 16000)", sampleRate)
	}
}

// getContextSize returns the context size based on sample rate
func getContextSize(sampleRate int64) int64 {
	if sampleRate == 16000 {
		return 64
	}
	return 32
}

// VoiceConfidence calculates voice activity confidence for the given audio buffer
func (v *SileroVAD) VoiceConfidence(audioBytes []byte, sampleRate int64) (float32, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if !v.initialized {
		return 0, fmt.Errorf("VAD not initialized")
	}

	// Check if we need to reset model state periodically
	if time.Since(v.lastResetTime).Seconds() >= MODEL_RESET_STATES_TIME {
		v.resetModelState()
		v.lastResetTime = time.Now()
	}

	// Convert audio bytes to float32 samples using preallocated buffer
	audioFloat32 := v.bytesToFloat32Prealloc(audioBytes)

	inputLength, err := getInputLength(sampleRate)
	if err != nil {
		return 0, err
	}

	// Initialize tensors if not done or if sample rate changed
	// This happens at most once for a given sample rate
	if v.session == nil || v.lastSampleRate != sampleRate {
		// Clean up old tensors if they exist
		if err := v.destroyTensors(); err != nil {
			return 0, err
		}

		// Create new tensors for this sample rate
		if err := v.createTensors(sampleRate, inputLength); err != nil {
			return 0, err
		}
		v.lastSampleRate = sampleRate
	}

	// Add new samples to buffer
	v.audioBuffer = append(v.audioBuffer, audioFloat32...)

	// If we don't have enough samples yet, return 0 confidence
	if int64(len(v.audioBuffer)) < inputLength {
		return 0.0, nil
	}

	// Ensure fullInput buffer is properly sized
	contextSize := getContextSize(sampleRate)
	expectedFullInputSize := int(contextSize + inputLength)

	// Reallocate fullInput if needed (should only happen once or on sample rate change)
	if cap(v.fullInput) < expectedFullInputSize {
		v.fullInput = make([]float32, expectedFullInputSize)
	} else {
		v.fullInput = v.fullInput[:expectedFullInputSize]
	}

	// Process complete chunks from buffer
	var lastConfidence float32
	for int64(len(v.audioBuffer)) >= inputLength {
		// Extract samples for processing
		samples := v.audioBuffer[:inputLength]
		v.audioBuffer = v.audioBuffer[inputLength:]

		// Initialize context if empty
		contextSize := getContextSize(sampleRate)
		if len(v.context) == 0 {
			v.context = make([]float32, contextSize)
		}

		// Build fullInput using preallocated buffer
		copy(v.fullInput[:contextSize], v.context)
		copy(v.fullInput[contextSize:], samples)

		// Get tensor data slices - these point to the underlying tensor memory
		// We're reusing the same tensor memory every time
		inputData := v.inputTensor.GetData()
		stateData := v.stateTensor.GetData()

		// Copy data into tensor memory
		copy(inputData, v.fullInput)
		copy(stateData, v.state)

		// Run inference - tensors are reused
		if err := v.session.Run(); err != nil {
			return 0, fmt.Errorf("inference failed: %w", err)
		}

		// Get speech probability from output tensor
		outputData := v.outputTensor.GetData()
		lastConfidence = outputData[0]

		// Update state from output tensor
		stateNData := v.stateNTensor.GetData()
		copy(v.state, stateNData)

		// Update context (keep last contextSize samples)
		copy(v.context, v.fullInput[len(v.fullInput)-int(contextSize):])
	}

	return lastConfidence, nil
}

// destroyTensors cleans up existing tensors
func (v *SileroVAD) destroyTensors() error {
	if v.session != nil {
		v.session.Destroy()
		v.session = nil
	}
	if v.inputTensor != nil {
		v.inputTensor.Destroy()
		v.inputTensor = nil
	}
	if v.srTensor != nil {
		v.srTensor.Destroy()
		v.srTensor = nil
	}
	if v.stateTensor != nil {
		v.stateTensor.Destroy()
		v.stateTensor = nil
	}
	if v.outputTensor != nil {
		v.outputTensor.Destroy()
		v.outputTensor = nil
	}
	if v.stateNTensor != nil {
		v.stateNTensor.Destroy()
		v.stateNTensor = nil
	}
	return nil
}

// resetModelState resets the internal model states
func (v *SileroVAD) resetModelState() {
	// Reset hidden state to zeros
	for i := range v.state {
		v.state[i] = 0.0
	}
	// Reset context
	for i := range v.context {
		v.context[i] = 0.0
	}
	// Clear audio buffer
	v.audioBuffer = v.audioBuffer[:0]
}

// Initialize sets up the ONNX runtime and session
func (v *SileroVAD) Initialize() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.initialized {
		return nil
	}

	// Initialize ONNX runtime environment exactly once for the process.
	// The environment is intentionally never destroyed because the ONNX
	// runtime leaks internal state when torn down and re-created.
	var envErr error
	onnxEnvOnce.Do(func() {
		ort.SetSharedLibraryPath(v.config.OnnxLibPath)
		envErr = ort.InitializeEnvironment()
	})
	if envErr != nil {
		return fmt.Errorf("failed to initialize ONNX environment: %w", envErr)
	}

	v.initialized = true
	return nil
}

// Close releases per-session resources (tensors, session) but leaves the
// global ONNX runtime environment intact for reuse by subsequent sessions.
func (v *SileroVAD) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if !v.initialized {
		return nil
	}

	v.destroyTensors()
	// NOTE: We intentionally do NOT call ort.DestroyEnvironment() here.
	// The ONNX runtime environment is a process-wide singleton that leaks
	// internal state when destroyed and re-created.
	v.initialized = false
	return nil
}

// createTensors creates the ONNX tensors and session - called once per sample rate
func (v *SileroVAD) createTensors(sampleRate, inputLength int64) error {
	// Calculate total input size (context + input)
	contextSize := getContextSize(sampleRate)
	totalInputSize := contextSize + inputLength

	// Create input tensor [batch, sequence_length]
	// This memory will be reused for every inference
	inputShape := ort.NewShape(1, totalInputSize)
	inputData := make([]float32, totalInputSize) // Tensor owns this memory
	inputTensor, err := ort.NewTensor(inputShape, inputData)
	if err != nil {
		return fmt.Errorf("failed to create input tensor: %w", err)
	}
	v.inputTensor = inputTensor

	// Create sample rate tensor [1] - constant, never changes
	srShape := ort.NewShape(1)
	srTensor, err := ort.NewTensor(srShape, []int64{sampleRate})
	if err != nil {
		return fmt.Errorf("failed to create sr tensor: %w", err)
	}
	v.srTensor = srTensor

	// Create state tensor [2, 1, 128]
	// This tensor's memory will be reused, but content updated each inference
	stateShape := ort.NewShape(2, 1, 128)
	stateTensor, err := ort.NewTensor(stateShape, v.state) // Uses v.state as backing memory
	if err != nil {
		return fmt.Errorf("failed to create state tensor: %w", err)
	}
	v.stateTensor = stateTensor

	// Create output tensor [1, 1] - will be filled by each inference
	outputShape := ort.NewShape(1, 1)
	outputTensor, err := ort.NewEmptyTensor[float32](outputShape)
	if err != nil {
		return fmt.Errorf("failed to create output tensor: %w", err)
	}
	v.outputTensor = outputTensor

	// Create stateN output tensor [2, 1, 128] - will be filled by each inference
	stateNShape := ort.NewShape(2, 1, 128)
	stateNTensor, err := ort.NewEmptyTensor[float32](stateNShape)
	if err != nil {
		return fmt.Errorf("failed to create stateN tensor: %w", err)
	}
	v.stateNTensor = stateNTensor

	// Create session - binds inputs and outputs
	// All tensors are now owned by the session and reused
	session, err := ort.NewAdvancedSession(
		v.config.OnnxPath,
		[]string{"input", "sr", "state"},                      // input names
		[]string{"output", "stateN"},                          // output names
		[]ort.Value{v.inputTensor, v.srTensor, v.stateTensor}, // inputs
		[]ort.Value{v.outputTensor, v.stateNTensor},           // outputs
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to create ONNX session: %w", err)
	}
	v.session = session

	return nil
}

// bytesToFloat32Prealloc converts raw audio bytes to float32 using preallocated buffers
func (v *SileroVAD) bytesToFloat32Prealloc(audioBytes []byte) []float32 {
	numSamples := len(audioBytes) / 2

	// Ensure conversion buffer has enough capacity
	if cap(v.conversionBuf) < numSamples {
		v.conversionBuf = make([]float32, numSamples, numSamples+64)
	} else {
		v.conversionBuf = v.conversionBuf[:numSamples]
	}

	// Ensure sample buffer has enough capacity
	if cap(v.sampleBuf) < numSamples {
		v.sampleBuf = make([]int16, numSamples, numSamples+64)
	} else {
		v.sampleBuf = v.sampleBuf[:numSamples]
	}

	// Convert bytes to int16 samples
	for i := range numSamples {
		v.sampleBuf[i] = int16(binary.LittleEndian.Uint16(audioBytes[i*2:]))
	}

	// Convert to float32 and normalize
	for i := range numSamples {
		v.conversionBuf[i] = float32(v.sampleBuf[i]) / 32768.0
	}

	return v.conversionBuf
}
