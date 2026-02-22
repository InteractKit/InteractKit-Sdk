package audio

/*
#cgo CFLAGS: -O2
#cgo LDFLAGS: -lsamplerate
#include "resample_bridge.h"
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"
)

// Buffer pools for memory reuse
var (
	floatBufferPool = sync.Pool{
		New: func() interface{} {
			return make([]float32, 0, 8192)
		},
	}
	byteBufferPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 0, 16384)
		},
	}
)

// getFloatBuffer retrieves a float32 buffer from the pool
func getFloatBuffer(size int) []float32 {
	buf := floatBufferPool.Get().([]float32)
	if cap(buf) < size {
		return make([]float32, size)
	}
	return buf[:size]
}

// putFloatBuffer returns a float32 buffer to the pool
func putFloatBuffer(buf []float32) {
	if cap(buf) <= 65536 { // Don't pool very large buffers
		floatBufferPool.Put(buf[:0])
	}
}

// getByteBuffer retrieves a byte buffer from the pool
func getByteBuffer(size int) []byte {
	buf := byteBufferPool.Get().([]byte)
	if cap(buf) < size {
		return make([]byte, size)
	}
	return buf[:size]
}

// putByteBuffer returns a byte buffer to the pool
func putByteBuffer(buf []byte) {
	if cap(buf) <= 131072 { // Don't pool very large buffers
		byteBufferPool.Put(buf[:0])
	}
}

// ResamplerQuality defines the quality/speed tradeoff for resampling
type ResamplerQuality int

const (
	QualityBest   ResamplerQuality = iota // Sinc best quality (slowest)
	QualityMedium                         // Sinc medium quality
	QualityFast                           // Sinc fastest
	QualityLinear                         // Linear interpolation
	QualityHold                           // Zero-order hold (fastest)
)

// Resampler handles sample rate conversion using libsamplerate
type Resampler struct {
	state    C.ResamplerState
	channels int
	ratio    float64
}

// NewResampler creates a new resampler instance
func NewResampler(channels int, quality ResamplerQuality, inputRate, outputRate int) (*Resampler, error) {
	if channels <= 0 || channels > 8 {
		return nil, fmt.Errorf("invalid channel count: %d", channels)
	}
	if inputRate <= 0 || outputRate <= 0 {
		return nil, fmt.Errorf("invalid sample rates: in=%d, out=%d", inputRate, outputRate)
	}

	ratio := float64(outputRate) / float64(inputRate)
	state := C.resampler_new(C.int(channels), C.int(quality), C.double(ratio))
	if state == nil {
		return nil, fmt.Errorf("failed to create resampler")
	}

	r := &Resampler{
		state:    state,
		channels: channels,
		ratio:    ratio,
	}

	// Ensure cleanup on GC
	runtime.SetFinalizer(r, (*Resampler).Close)
	return r, nil
}

// Resample converts PCM int16 data from input rate to output rate
func (r *Resampler) Resample(inputPCM []byte) ([]byte, error) {
	if len(inputPCM) == 0 {
		return nil, nil
	}
	if len(inputPCM)%(2*r.channels) != 0 {
		return nil, fmt.Errorf("input PCM length %d invalid for %d channels", len(inputPCM), r.channels)
	}

	inputSamples := len(inputPCM) / 2
	inputFrames := inputSamples / r.channels

	// Calculate output size and allocate
	outputFrames := int(C.resampler_get_output_len(r.state, C.long(inputFrames)))
	if outputFrames <= 0 {
		return nil, fmt.Errorf("invalid output frame calculation")
	}

	// Allocate buffers from pools
	inputFloat := getFloatBuffer(inputSamples)
	defer putFloatBuffer(inputFloat)

	outputFloat := getFloatBuffer(outputFrames * r.channels)
	defer putFloatBuffer(outputFloat)

	// Convert int16 to float
	C.pcm16_to_float(
		(*C.int16_t)(unsafe.Pointer(&inputPCM[0])),
		(*C.float)(&inputFloat[0]),
		C.int(inputSamples),
	)

	// Process resampling
	outputFramesProduced := int(C.resampler_process(
		r.state,
		(*C.float)(&inputFloat[0]),
		C.long(inputFrames),
		(*C.float)(&outputFloat[0]),
		C.long(outputFrames),
	))

	if outputFramesProduced < 0 {
		return nil, fmt.Errorf("resampling failed")
	}

	// Convert float back to int16
	outputSamples := outputFramesProduced * r.channels
	outputPCM := getByteBuffer(outputSamples * 2)
	defer putByteBuffer(outputPCM)

	C.float_to_pcm16(
		(*C.float)(&outputFloat[0]),
		(*C.int16_t)(unsafe.Pointer(&outputPCM[0])),
		C.int(outputSamples),
	)

	// Return a copy since we're returning the buffer to pool
	result := make([]byte, outputSamples*2)
	copy(result, outputPCM)
	return result, nil
}

// Reset clears the internal state of the resampler
func (r *Resampler) Reset() {
	if r.state != nil {
		C.resampler_reset(r.state)
	}
}

// Close frees the resampler resources
func (r *Resampler) Close() error {
	if r.state != nil {
		C.resampler_free(r.state)
		r.state = nil
	}
	return nil
}

// ResamplePCMBytes resamples PCM data using a pooled Resampler.
// NOTE: pooled resamplers accumulate internal filter state across calls which
// is correct for streaming (consecutive chunks from the same source). For
// unrelated audio the tiny filter tail from a previous caller is negligible
// at ≤ a few samples and inaudible in practice.
func ResamplePCMBytes(pcm []byte, channels, inputRate, outputRate int, quality ResamplerQuality) ([]byte, error) {
	key := fmt.Sprintf("rs-%d-%d-%d-%d", channels, inputRate, outputRate, quality)
	pool := getResamplerPool(key, func() interface{} {
		r, err := NewResampler(channels, quality, inputRate, outputRate)
		if err != nil {
			return nil
		}
		runtime.SetFinalizer(r, nil)
		return r
	})

	v := pool.Get()
	if v == nil {
		return nil, fmt.Errorf("failed to create pooled resampler")
	}
	resampler := v.(*Resampler)

	result, err := resampler.Resample(pcm)
	// Reset internal filter state before returning to pool so the next
	// borrower starts with a clean resampler.
	resampler.Reset()
	pool.Put(resampler)
	return result, err
}

// resamplerPools mirrors codecPools in audio.go for Resampler instances.
var resamplerPools sync.Map

func getResamplerPool(key string, newFn func() interface{}) *sync.Pool {
	if v, ok := resamplerPools.Load(key); ok {
		return v.(*sync.Pool)
	}
	pool := &sync.Pool{New: newFn}
	actual, _ := resamplerPools.LoadOrStore(key, pool)
	return actual.(*sync.Pool)
}
