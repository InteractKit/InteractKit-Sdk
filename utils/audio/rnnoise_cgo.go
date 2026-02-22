package audio

/*
#cgo CFLAGS: -I/usr/local/include -I/usr/include
#cgo LDFLAGS: -lrnnoise -lm
#include "rnnoise_bridge.h"
#include <stdlib.h>
*/
import "C"
import (
	"fmt"
	"interactkit/core"
	"runtime"
	"sync"
	"unsafe"
)

const (
	// rnnoiseTargetSampleRate is the only sample rate RNNoise accepts.
	rnnoiseTargetSampleRate = 48000
	// rnnoiseFrameSize is the fixed number of mono samples per RNNoise frame (10ms at 48kHz).
	rnnoiseFrameSize = 480
)

// RNNoiseDenoiser applies real-time noise suppression using the RNNoise library.
//
// RNNoise processes 16-bit mono PCM at exactly 48 kHz in fixed 480-sample (10ms)
// frames. This denoiser handles all necessary conversions transparently:
//   - stereo → mono before processing, mono → stereo after
//   - sample-rate resampling to/from 48 kHz via libsamplerate
//   - frame-boundary buffering so callers can supply arbitrary chunk sizes
//
// The denoiser is stateful; create one instance per audio stream so that the
// internal RNNoise model can accumulate temporal context across frames.
type RNNoiseDenoiser struct {
	state       *C.DenoiseState
	inputSR     int
	inputCh     int
	upsampler   *Resampler // inputSR → 48 kHz (nil when inputSR == 48000)
	downsampler *Resampler // 48 kHz → inputSR (nil when inputSR == 48000)
	// inBuf accumulates mono float32 samples at 48 kHz waiting for a full frame.
	// At most rnnoiseFrameSize-1 samples remain between calls.
	inBuf  []float32
	outBuf []float32 // reused scratch buffer for one processed frame
	mu     sync.Mutex
}

// NewRNNoiseDenoiser creates a denoiser configured for audio at sampleRate Hz
// with the given number of channels (1 or 2).
func NewRNNoiseDenoiser(sampleRate, channels int) (*RNNoiseDenoiser, error) {
	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("rnnoise: unsupported channel count %d (must be 1 or 2)", channels)
	}
	if sampleRate <= 0 {
		return nil, fmt.Errorf("rnnoise: invalid sample rate %d", sampleRate)
	}

	state := C.rnnoise_bridge_create()
	if state == nil {
		return nil, fmt.Errorf("rnnoise: failed to allocate DenoiseState")
	}

	d := &RNNoiseDenoiser{
		state:   state,
		inputSR: sampleRate,
		inputCh: channels,
		inBuf:   make([]float32, 0, rnnoiseFrameSize*4),
		outBuf:  make([]float32, rnnoiseFrameSize),
	}

	if sampleRate != rnnoiseTargetSampleRate {
		up, err := NewResampler(1, QualityFast, sampleRate, rnnoiseTargetSampleRate)
		if err != nil {
			C.rnnoise_bridge_destroy(state)
			return nil, fmt.Errorf("rnnoise: failed to create upsampler: %w", err)
		}
		down, err := NewResampler(1, QualityFast, rnnoiseTargetSampleRate, sampleRate)
		if err != nil {
			_ = up.Close()
			C.rnnoise_bridge_destroy(state)
			return nil, fmt.Errorf("rnnoise: failed to create downsampler: %w", err)
		}
		d.upsampler = up
		d.downsampler = down
	}

	runtime.SetFinalizer(d, (*RNNoiseDenoiser).Close)
	return d, nil
}

// Denoise suppresses noise in raw 16-bit little-endian PCM bytes.
//
// The input must match the sampleRate and channels used when creating the
// denoiser. The output is PCM in the same format.
//
// Because RNNoise processes fixed 10ms frames, this call may return fewer
// bytes than were supplied if the accumulated samples do not yet fill a
// complete frame. Those samples are held internally and flushed in a
// subsequent call. Callers that need a final flush should call Flush after
// the last chunk.
func (d *RNNoiseDenoiser) Denoise(pcm []byte) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(pcm) == 0 {
		return []byte{}, nil
	}
	if len(pcm)%2 != 0 {
		return nil, fmt.Errorf("rnnoise: PCM data must have even byte length")
	}

	// 1. Stereo → mono.
	workPCM := pcm
	if d.inputCh == 2 {
		workPCM = stereoToMono(pcm)
	}

	// 2. Resample to 48 kHz (if required).
	monoAt48k, err := d.toTargetRate(workPCM)
	if err != nil {
		return nil, err
	}

	// 3. int16 → float32 (raw int16 scale expected by RNNoise).
	numSamples := len(monoAt48k) / 2
	inFloat := make([]float32, numSamples)
	if numSamples > 0 {
		C.rnnoise_bridge_pcm16_to_float(
			(*C.int16_t)(unsafe.Pointer(&monoAt48k[0])),
			(*C.float)(unsafe.Pointer(&inFloat[0])),
			C.int(numSamples),
		)
	}
	d.inBuf = append(d.inBuf, inFloat...)

	// 4. Process all complete 480-sample frames.
	outFloat := make([]float32, 0, len(d.inBuf))
	for len(d.inBuf) >= rnnoiseFrameSize {
		frame := d.inBuf[:rnnoiseFrameSize]
		C.rnnoise_bridge_process_frame(
			d.state,
			(*C.float)(unsafe.Pointer(&d.outBuf[0])),
			(*C.float)(unsafe.Pointer(&frame[0])),
		)
		outFloat = append(outFloat, d.outBuf...)
		d.inBuf = d.inBuf[rnnoiseFrameSize:]
	}

	if len(outFloat) == 0 {
		return []byte{}, nil
	}

	// 5. float32 → int16.
	outPCM48k := make([]byte, len(outFloat)*2)
	C.rnnoise_bridge_float_to_pcm16(
		(*C.float)(unsafe.Pointer(&outFloat[0])),
		(*C.int16_t)(unsafe.Pointer(&outPCM48k[0])),
		C.int(len(outFloat)),
	)

	// 6. Downsample back to the original rate (if required).
	resultMono, err := d.fromTargetRate(outPCM48k)
	if err != nil {
		return nil, err
	}

	// 7. Mono → stereo.
	if d.inputCh == 2 {
		return monoToStereo(resultMono), nil
	}
	return resultMono, nil
}

// DenoiseChunk applies noise suppression to a core.AudioChunk.
// The chunk must use PCM encoding; other formats are rejected.
// The returned chunk preserves the original SampleRate, Channels, and Timestamp.
func (d *RNNoiseDenoiser) DenoiseChunk(chunk core.AudioChunk) (core.AudioChunk, error) {
	if chunk.Format != core.PCM {
		return core.AudioChunk{}, fmt.Errorf("rnnoise: chunk must be PCM format")
	}
	if chunk.Data == nil || len(*chunk.Data) == 0 {
		return chunk, nil
	}
	if chunk.SampleRate != d.inputSR || chunk.Channels != d.inputCh {
		return core.AudioChunk{}, fmt.Errorf(
			"rnnoise: chunk has rate=%d ch=%d but denoiser was created for rate=%d ch=%d",
			chunk.SampleRate, chunk.Channels, d.inputSR, d.inputCh,
		)
	}

	denoised, err := d.Denoise(*chunk.Data)
	if err != nil {
		return core.AudioChunk{}, err
	}

	return core.AudioChunk{
		Data:       &denoised,
		SampleRate: chunk.SampleRate,
		Channels:   chunk.Channels,
		Format:     core.PCM,
		Timestamp:  chunk.Timestamp,
	}, nil
}

// Flush processes any samples remaining in the internal frame buffer by
// zero-padding the last partial frame. Call this after the final audio chunk
// to retrieve all denoised output.
func (d *RNNoiseDenoiser) Flush() ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.inBuf) == 0 {
		return []byte{}, nil
	}

	// Zero-pad to a complete frame.
	padded := make([]float32, rnnoiseFrameSize)
	copy(padded, d.inBuf)
	d.inBuf = d.inBuf[:0]

	C.rnnoise_bridge_process_frame(
		d.state,
		(*C.float)(unsafe.Pointer(&d.outBuf[0])),
		(*C.float)(unsafe.Pointer(&padded[0])),
	)

	// float32 → int16.
	outPCM48k := make([]byte, rnnoiseFrameSize*2)
	C.rnnoise_bridge_float_to_pcm16(
		(*C.float)(unsafe.Pointer(&d.outBuf[0])),
		(*C.int16_t)(unsafe.Pointer(&outPCM48k[0])),
		C.int(rnnoiseFrameSize),
	)

	resultMono, err := d.fromTargetRate(outPCM48k)
	if err != nil {
		return nil, err
	}

	if d.inputCh == 2 {
		return monoToStereo(resultMono), nil
	}
	return resultMono, nil
}

// Reset clears the internal frame buffer and resets the resampler states.
// The RNNoise model state is NOT reset; use Close + NewRNNoiseDenoiser for that.
func (d *RNNoiseDenoiser) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.inBuf = d.inBuf[:0]
	if d.upsampler != nil {
		d.upsampler.Reset()
	}
	if d.downsampler != nil {
		d.downsampler.Reset()
	}
}

// Close releases all resources held by the denoiser.
func (d *RNNoiseDenoiser) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.state != nil {
		C.rnnoise_bridge_destroy(d.state)
		d.state = nil
	}
	if d.upsampler != nil {
		_ = d.upsampler.Close()
		d.upsampler = nil
	}
	if d.downsampler != nil {
		_ = d.downsampler.Close()
		d.downsampler = nil
	}
	return nil
}

// toTargetRate resamples mono PCM from d.inputSR to 48 kHz.
// Returns the input unchanged if no resampling is needed.
func (d *RNNoiseDenoiser) toTargetRate(pcm []byte) ([]byte, error) {
	if d.inputSR == rnnoiseTargetSampleRate {
		return pcm, nil
	}
	out, err := d.upsampler.Resample(pcm)
	if err != nil {
		return nil, fmt.Errorf("rnnoise: upsampling failed: %w", err)
	}
	return out, nil
}

// fromTargetRate resamples mono PCM from 48 kHz back to d.inputSR.
// Returns the input unchanged if no resampling is needed.
func (d *RNNoiseDenoiser) fromTargetRate(pcm []byte) ([]byte, error) {
	if d.inputSR == rnnoiseTargetSampleRate {
		return pcm, nil
	}
	out, err := d.downsampler.Resample(pcm)
	if err != nil {
		return nil, fmt.Errorf("rnnoise: downsampling failed: %w", err)
	}
	return out, nil
}

// DenoiseAudioChunk is a convenience wrapper for one-off noise suppression of
// a single chunk. For streaming audio, prefer creating a persistent
// RNNoiseDenoiser so that temporal model state is preserved across chunks.
func DenoiseAudioChunk(chunk core.AudioChunk) (core.AudioChunk, error) {
	if chunk.Format != core.PCM {
		return core.AudioChunk{}, fmt.Errorf("rnnoise: chunk must be PCM format")
	}
	d, err := NewRNNoiseDenoiser(chunk.SampleRate, chunk.Channels)
	if err != nil {
		return core.AudioChunk{}, err
	}
	defer d.Close()
	return d.DenoiseChunk(chunk)
}
