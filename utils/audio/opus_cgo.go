package audio

/*
#cgo CFLAGS: -I/usr/local/include
#cgo LDFLAGS: -lopus -lm
#include "opus_encoder.h"
#include <stdlib.h>
*/
import "C"
import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"
)

// OpusApplication defines the encoding quality/application type
type OpusApplication int

const (
	OpusAppAudio              OpusApplication = C.OPUS_APPLICATION_AUDIO
	OpusAppVoIP               OpusApplication = C.OPUS_APPLICATION_VOIP
	OpusAppRestrictedLowDelay OpusApplication = C.OPUS_APPLICATION_RESTRICTED_LOWDELAY
)

// OpusError represents Opus library errors
type OpusError int

func (e OpusError) Error() string {
	return C.GoString(C.opus_strerror(C.int(e)))
}

// CgoOpusEncoder is a CGO-based Opus encoder
type CgoOpusEncoder struct {
	handle        *C.OpusEncoderHandle
	sampleRate    int
	channels      int
	frameSize     int
	maxPacketSize int
	mu            sync.Mutex
}

// NewCgoOpusEncoder creates a new CGO Opus encoder
func NewCgoOpusEncoder(sampleRate, channels int, app OpusApplication) (*CgoOpusEncoder, error) {
	var err C.int
	handle := C.wrapper_opus_encoder_create(C.int(sampleRate), C.int(channels), C.int(app), &err)

	if err != 0 {
		return nil, fmt.Errorf("failed to create Opus encoder: %w", OpusError(err))
	}

	// Calculate frame size (60ms max as per your constant)
	frameSize := sampleRate * opusFrameSizeMs / 1000

	enc := &CgoOpusEncoder{
		handle:        handle,
		sampleRate:    sampleRate,
		channels:      channels,
		frameSize:     frameSize,
		maxPacketSize: opusMaxPacketSize,
	}

	// Ensure cleanup on GC if Close() is not called
	runtime.SetFinalizer(enc, (*CgoOpusEncoder).Close)
	return enc, nil
}

// Encode encodes PCM samples to Opus
func (e *CgoOpusEncoder) Encode(pcm []int16) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == nil {
		return nil, fmt.Errorf("encoder already closed")
	}

	if len(pcm) == 0 {
		return nil, fmt.Errorf("empty PCM data")
	}

	// Allocate output buffer
	outBuf := make([]byte, e.maxPacketSize)

	// Encode directly without copying
	n := C.wrapper_opus_encode(
		e.handle,
		(*C.int16_t)(unsafe.Pointer(&pcm[0])),
		C.int(len(pcm)/e.channels), // frame size in samples per channel
		(*C.uchar)(unsafe.Pointer(&outBuf[0])),
		C.int(e.maxPacketSize),
	)

	if n < 0 {
		return nil, fmt.Errorf("opus encode failed: %w", OpusError(n))
	}

	return outBuf[:n], nil
}

// EncodeBytes encodes PCM bytes directly to Opus
func (e *CgoOpusEncoder) EncodeBytes(pcm []byte) ([]byte, error) {
	if len(pcm)%2 != 0 {
		return nil, fmt.Errorf("PCM bytes must have even length")
	}

	// Zero-copy conversion from []byte to []int16
	samples := unsafe.Slice((*int16)(unsafe.Pointer(&pcm[0])), len(pcm)/2)
	return e.Encode(samples)
}

// Close releases encoder resources
func (e *CgoOpusEncoder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle != nil {
		C.wrapper_opus_encoder_destroy(e.handle)
		e.handle = nil
	}
	return nil
}

// CgoOpusDecoder is a CGO-based Opus decoder
type CgoOpusDecoder struct {
	handle     *C.OpusDecoderHandle
	sampleRate int
	channels   int
	frameSize  int
	mu         sync.Mutex
}

// NewCgoOpusDecoder creates a new CGO Opus decoder
func NewCgoOpusDecoder(sampleRate, channels int) (*CgoOpusDecoder, error) {
	var err C.int
	handle := C.wrapper_opus_decoder_create(C.int(sampleRate), C.int(channels), &err)

	if err != 0 {
		return nil, fmt.Errorf("failed to create Opus decoder: %w", OpusError(err))
	}

	// Max frame size (60ms)
	frameSize := sampleRate * opusFrameSizeMs / 1000 * channels

	dec := &CgoOpusDecoder{
		handle:     handle,
		sampleRate: sampleRate,
		channels:   channels,
		frameSize:  frameSize,
	}

	// Ensure cleanup on GC if Close() is not called
	runtime.SetFinalizer(dec, (*CgoOpusDecoder).Close)
	return dec, nil
}

// Decode decodes Opus data to PCM samples
func (d *CgoOpusDecoder) Decode(opusData []byte) ([]int16, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle == nil {
		return nil, fmt.Errorf("decoder already closed")
	}

	// Allocate output buffer
	pcmSamples := make([]int16, d.frameSize)

	var n C.int
	if len(opusData) == 0 {
		// PLC mode (packet loss concealment)
		n = C.wrapper_opus_decode(d.handle, nil, 0,
			(*C.int16_t)(unsafe.Pointer(&pcmSamples[0])),
			C.int(d.frameSize/d.channels), 0)
	} else {
		n = C.wrapper_opus_decode(d.handle,
			(*C.uchar)(unsafe.Pointer(&opusData[0])),
			C.int(len(opusData)),
			(*C.int16_t)(unsafe.Pointer(&pcmSamples[0])),
			C.int(d.frameSize/d.channels), 0)
	}

	if n < 0 {
		return nil, fmt.Errorf("opus decode failed: %w", OpusError(n))
	}

	if n == 0 {
		return []int16{}, nil
	}

	// Return only the decoded samples
	return pcmSamples[:int(n)*d.channels], nil
}

// DecodeToBytes decodes Opus data to PCM bytes
func (d *CgoOpusDecoder) DecodeToBytes(opusData []byte) ([]byte, error) {
	samples, err := d.Decode(opusData)
	if err != nil {
		return nil, err
	}

	if len(samples) == 0 {
		return []byte{}, nil
	}

	// Zero-copy conversion from []int16 to []byte
	bytes := unsafe.Slice((*byte)(unsafe.Pointer(&samples[0])), len(samples)*2)

	// Need to copy because the underlying array will be freed
	result := make([]byte, len(bytes))
	copy(result, bytes)
	return result, nil
}

// Close releases decoder resources
func (d *CgoOpusDecoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle != nil {
		C.wrapper_opus_decoder_destroy(d.handle)
		d.handle = nil
	}
	return nil
}

// OpusPacketInfo contains information about an Opus packet
type OpusPacketInfo struct {
	SamplesPerFrame int
	Channels        int
	FrameCount      int
}

// GetOpusPacketInfo extracts information from an Opus packet
func GetOpusPacketInfo(opusData []byte, sampleRate int) (*OpusPacketInfo, error) {
	if len(opusData) == 0 {
		return nil, fmt.Errorf("empty Opus data")
	}

	samplesPerFrame := int(C.wrapper_opus_packet_get_samples_per_frame(
		(*C.uchar)(unsafe.Pointer(&opusData[0])),
		C.int(sampleRate)))

	if samplesPerFrame <= 0 {
		return nil, fmt.Errorf("failed to get samples per frame")
	}

	channels := int(C.wrapper_opus_packet_get_nb_channels(
		(*C.uchar)(unsafe.Pointer(&opusData[0]))))

	frameCount := int(C.wrapper_opus_packet_get_nb_frames(
		(*C.uchar)(unsafe.Pointer(&opusData[0])),
		C.int(len(opusData))))

	return &OpusPacketInfo{
		SamplesPerFrame: samplesPerFrame,
		Channels:        channels,
		FrameCount:      frameCount,
	}, nil
}
