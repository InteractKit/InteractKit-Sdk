package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"interactkit/core"
	"runtime"
	"sync"

	"github.com/zaf/g711"
)

// PCM constants
const (
	pcmMax = 32767  // Max 16-bit PCM value
	pcmMin = -32768 // Min 16-bit PCM value
)

// Opus constants
const (
	opusMaxPacketSize = 4000 // Maximum Opus packet size in bytes
	opusFrameSizeMs   = 60   // Maximum frame size in milliseconds
)

// Buffer pools for frequently used operations
var (
	// Pool for WAV header buffers (typically 44-46 bytes)
	wavHeaderPool = sync.Pool{
		New: func() interface{} {
			return bytes.NewBuffer(make([]byte, 0, 64))
		},
	}

	// Pool for temporary buffers used in channel conversion
	channelConvPool = sync.Pool{
		New: func() interface{} {
			// Start with 4KB buffer, will grow if needed
			return make([]byte, 0, 4096)
		},
	}

	// Pool for sample buffers (16-bit samples)
	sampleBufferPool = sync.Pool{
		New: func() interface{} {
			// Pool for sample conversion buffers
			return make([]byte, 0, 2048)
		},
	}

	// Pool for resampling intermediate buffers
	resampleBufferPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 0, 8192)
		},
	}

	// Pool for uint16 conversion buffers
	uint16BufferPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 2)
		},
	}
)

// codecPoolMap holds sync.Pool instances for Opus encoders, decoders, and
// resamplers keyed by their config string.  Pools are created lazily on first
// access and reused for the process lifetime so CGO handles are recycled
// instead of allocated/freed per audio chunk.
var codecPools sync.Map // map[string]*sync.Pool

// getCodecPool returns (or lazily creates) a *sync.Pool for the given key.
func getCodecPool(key string, newFn func() interface{}) *sync.Pool {
	if v, ok := codecPools.Load(key); ok {
		return v.(*sync.Pool)
	}
	pool := &sync.Pool{New: newFn}
	actual, _ := codecPools.LoadOrStore(key, pool)
	return actual.(*sync.Pool)
}

// getWavHeaderBuffer retrieves a buffer from the WAV header pool
func getWavHeaderBuffer() *bytes.Buffer {
	return wavHeaderPool.Get().(*bytes.Buffer)
}

// putWavHeaderBuffer returns a buffer to the WAV header pool
func putWavHeaderBuffer(buf *bytes.Buffer) {
	buf.Reset()
	wavHeaderPool.Put(buf)
}

// getChannelConvBuffer retrieves a buffer from the channel conversion pool
func getChannelConvBuffer(capacity int) []byte {
	buf := channelConvPool.Get().([]byte)
	if cap(buf) < capacity {
		// If capacity is insufficient, allocate a new buffer
		return make([]byte, capacity)
	}
	return buf[:0] // Reset length but keep capacity
}

// putChannelConvBuffer returns a buffer to the channel conversion pool
func putChannelConvBuffer(buf []byte) {
	if cap(buf) <= 32768 { // Don't pool very large buffers
		channelConvPool.Put(buf)
	}
}

// getUint16Buffer retrieves a 2-byte buffer for uint16 operations
func getUint16Buffer() []byte {
	return uint16BufferPool.Get().([]byte)
}

// putUint16Buffer returns a 2-byte buffer to the pool
func putUint16Buffer(buf []byte) {
	uint16BufferPool.Put(buf)
}

// PCMToULaw converts a 16-bit PCM sample to 8-bit µ-law using ITU-T G.711 standard
func PCMToULaw(sample int16) byte {
	return g711.EncodeUlawFrame(sample)
}

// ULawToPCM converts an 8-bit µ-law byte to 16-bit PCM using ITU-T G.711 standard
func ULawToPCM(u byte) int16 {
	return g711.DecodeUlawFrame(u)
}

// PCMBytesToULaw converts PCM bytes to µ-law
func PCMBytesToULaw(pcm []byte) ([]byte, error) {
	if len(pcm)%2 != 0 {
		return nil, errors.New("PCM byte slice length must be even (16-bit samples)")
	}
	return g711.EncodeUlaw(pcm), nil
}

// ULawBytesToPCM converts µ-law bytes to PCM bytes
func ULawBytesToPCM(uBytes []byte) []byte {
	return g711.DecodeUlaw(uBytes)
}

// PCMToALaw converts a 16-bit PCM sample to 8-bit A-law using ITU-T G.711 standard
func PCMToALaw(sample int16) byte {
	return g711.EncodeAlawFrame(sample)
}

// ALawToPCM converts an 8-bit A-law byte to 16-bit PCM using ITU-T G.711 standard
func ALawToPCM(a byte) int16 {
	return g711.DecodeAlawFrame(a)
}

// PCMBytesToALaw converts PCM bytes to A-law
func PCMBytesToALaw(pcm []byte) ([]byte, error) {
	if len(pcm)%2 != 0 {
		return nil, errors.New("PCM byte slice length must be even (16-bit samples)")
	}
	return g711.EncodeAlaw(pcm), nil
}

// ALawBytesToPCM converts A-law bytes to PCM bytes
func ALawBytesToPCM(aBytes []byte) []byte {
	return g711.DecodeAlaw(aBytes)
}

// PCMBytesToOpus encodes PCM to Opus using a pooled CGO encoder.
func PCMBytesToOpus(pcm []byte, sampleRate, channels int) ([]byte, error) {
	if err := ValidatePCMData(pcm, channels); err != nil {
		return nil, err
	}

	key := fmt.Sprintf("enc-%d-%d", sampleRate, channels)
	pool := getCodecPool(key, func() interface{} {
		enc, err := NewCgoOpusEncoder(sampleRate, channels, OpusAppAudio)
		if err != nil {
			return nil // pool will call New again on next Get
		}
		// Remove the GC finalizer — the pool manages the lifetime.
		runtime.SetFinalizer(enc, nil)
		return enc
	})

	v := pool.Get()
	if v == nil {
		return nil, fmt.Errorf("failed to create pooled Opus encoder")
	}
	encoder := v.(*CgoOpusEncoder)
	defer pool.Put(encoder)

	return encoder.EncodeBytes(pcm)
}

// OpusBytesToPCM decodes Opus to PCM using a pooled CGO decoder.
func OpusBytesToPCM(opusData []byte, sampleRate, channels int) ([]byte, error) {
	if len(opusData) == 0 {
		return nil, fmt.Errorf("empty Opus data")
	}

	key := fmt.Sprintf("dec-%d-%d", sampleRate, channels)
	pool := getCodecPool(key, func() interface{} {
		dec, err := NewCgoOpusDecoder(sampleRate, channels)
		if err != nil {
			return nil
		}
		runtime.SetFinalizer(dec, nil)
		return dec
	})

	v := pool.Get()
	if v == nil {
		return nil, fmt.Errorf("failed to create pooled Opus decoder")
	}
	decoder := v.(*CgoOpusDecoder)
	defer pool.Put(decoder)

	return decoder.DecodeToBytes(opusData)
}

// PCMBytesToWavBytes wraps PCM []byte into WAV []byte (16-bit little endian)
// Supports mono or stereo with buffer pooling
func PCMBytesToWavBytes(pcm []byte, numChannels, sampleRate int) ([]byte, error) {
	if len(pcm) == 0 {
		return nil, errors.New("PCM data is empty")
	}
	if numChannels <= 0 || numChannels > 2 {
		return nil, errors.New("only mono (1) or stereo (2) channels supported")
	}
	if sampleRate <= 0 {
		return nil, errors.New("sample rate must be positive")
	}
	if len(pcm)%(2*numChannels) != 0 {
		return nil, errors.New("PCM data length doesn't match channel count")
	}

	// Get buffer from pool
	buf := getWavHeaderBuffer()
	defer putWavHeaderBuffer(buf)

	// WAV format constants
	const (
		bitsPerSample  = 16
		audioFormatPCM = 1
		subchunk1Size  = 16
	)

	blockAlign := numChannels * bitsPerSample / 8
	byteRate := sampleRate * blockAlign
	dataSize := len(pcm)
	fileSize := 36 + dataSize // 36 = WAV header size

	// Write RIFF header
	buf.WriteString("RIFF")
	binary.Write(buf, binary.LittleEndian, uint32(fileSize))
	buf.WriteString("WAVE")

	// Write fmt sub-chunk
	buf.WriteString("fmt ")
	binary.Write(buf, binary.LittleEndian, uint32(subchunk1Size))
	binary.Write(buf, binary.LittleEndian, uint16(audioFormatPCM))
	binary.Write(buf, binary.LittleEndian, uint16(numChannels))
	binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	binary.Write(buf, binary.LittleEndian, uint32(byteRate))
	binary.Write(buf, binary.LittleEndian, uint16(blockAlign))
	binary.Write(buf, binary.LittleEndian, uint16(bitsPerSample))

	// Write data sub-chunk
	buf.WriteString("data")
	binary.Write(buf, binary.LittleEndian, uint32(dataSize))

	// Combine header and PCM data
	result := make([]byte, buf.Len()+len(pcm))
	copy(result, buf.Bytes())
	copy(result[buf.Len():], pcm)

	return result, nil
}

// ValidatePCMData validates PCM byte array for basic integrity
func ValidatePCMData(pcm []byte, numChannels int) error {
	if len(pcm)%2 != 0 {
		return errors.New("PCM data must have even length (16-bit samples)")
	}
	if len(pcm) == 0 {
		return errors.New("PCM data is empty")
	}
	if numChannels <= 0 {
		return errors.New("invalid number of channels")
	}
	if len(pcm)%(2*numChannels) != 0 {
		return errors.New("PCM data length doesn't match channel count")
	}
	return nil
}

// GetPCMSampleCount returns number of 16-bit samples in PCM data
func GetPCMSampleCount(pcm []byte) int {
	if len(pcm)%2 != 0 {
		return 0
	}
	return len(pcm) / 2
}

// GetPCMDurationSeconds returns duration in seconds
func GetPCMDurationSeconds(pcm []byte, numChannels, sampleRate int) (float64, error) {
	if err := ValidatePCMData(pcm, numChannels); err != nil {
		return 0, err
	}
	if sampleRate <= 0 {
		return 0, errors.New("invalid sample rate")
	}

	sampleCount := GetPCMSampleCount(pcm)
	frameCount := sampleCount / numChannels
	return float64(frameCount) / float64(sampleRate), nil
}

// StripWAVHeaderIfPresent returns raw PCM bytes if input starts with a RIFF/WAVE header.
// If the input is not a WAV file, it returns the input unchanged.
// Only extracts the "data" chunk and ignores other subchunks.
func StripWAVHeaderIfPresent(chunk []byte) ([]byte, error) {
	// Minimum RIFF header size: 12 bytes ("RIFF" + size + "WAVE")
	if len(chunk) < 12 {
		return chunk, nil
	}
	if !bytes.HasPrefix(chunk, []byte("RIFF")) || !bytes.Equal(chunk[8:12], []byte("WAVE")) {
		return chunk, nil
	}

	i := 12
	for i+8 <= len(chunk) {
		chunkID := string(chunk[i : i+4])
		chunkSize := binary.LittleEndian.Uint32(chunk[i+4 : i+8])
		next := i + 8 + int(chunkSize)

		if chunkID == "data" {
			if next > len(chunk) {
				return nil, errors.New("invalid WAV: data chunk exceeds buffer length")
			}
			return chunk[i+8 : next], nil
		}

		// Account for padding to even boundary
		if chunkSize%2 != 0 {
			next++
		}
		if next > len(chunk) {
			break
		}
		i = next
	}

	return nil, errors.New("invalid WAV: data chunk not found")
}

// ConvertAudioChunk converts audio data between different formats, sample rates, and channel counts
func ConvertAudioChunk(
	input core.AudioChunk,
	targetFormat core.AudioEncodingFormat,
	targetChannels int,
	targetSampleRate int,
) (core.AudioChunk, error) {
	needToConvertFormat := input.Format != targetFormat
	needToConvertSampleRate := input.SampleRate != targetSampleRate
	needToConvertChannels := input.Channels != targetChannels

	if !needToConvertFormat && !needToConvertSampleRate && !needToConvertChannels {
		return input, nil
	}

	// First convert everything to PCM as intermediate format
	if input.Format != core.PCM {
		pcmBytes, err := convertToPCM(input)
		if err != nil {
			return core.AudioChunk{}, err
		}
		input.Data = &pcmBytes
		input.Format = core.PCM
	}

	// Handle channel conversion (mono/stereo) if needed
	if needToConvertChannels {
		pcmBytes, err := convertChannels(*input.Data, input.Channels, targetChannels)
		if err != nil {
			return core.AudioChunk{}, err
		}
		input.Data = &pcmBytes
		input.Channels = targetChannels
	}

	// Handle sample rate conversion if needed
	if needToConvertSampleRate {
		resampledBytes, err := ResamplePCMBytes(*input.Data, input.Channels, input.SampleRate, targetSampleRate, QualityLinear)
		if err != nil {
			return core.AudioChunk{}, err
		}
		input.Data = &resampledBytes
		input.SampleRate = targetSampleRate
	}

	// Convert to target format if needed
	if needToConvertFormat && targetFormat != core.PCM {
		convertedBytes, err := convertFromPCM(*input.Data, input.Channels, input.SampleRate, targetFormat)
		if err != nil {
			return core.AudioChunk{}, err
		}
		input.Data = &convertedBytes
		input.Format = targetFormat
	}

	return input, nil
}

// convertToPCM converts various audio formats to PCM
func convertToPCM(input core.AudioChunk) ([]byte, error) {
	switch input.Format {
	case core.ULAW:
		return ULawBytesToPCM(*input.Data), nil
	case core.ALAW:
		return ALawBytesToPCM(*input.Data), nil
	case core.OPUS:
		return OpusBytesToPCM(*input.Data, input.SampleRate, input.Channels)
	default:
		return nil, errors.New("unsupported format for PCM conversion")
	}
}

// convertFromPCM converts PCM to target format
func convertFromPCM(pcm []byte, channels, sampleRate int, targetFormat core.AudioEncodingFormat) ([]byte, error) {
	switch targetFormat {
	case core.ULAW:
		return PCMBytesToULaw(pcm)
	case core.ALAW:
		return PCMBytesToALaw(pcm)
	case core.OPUS:
		return PCMBytesToOpus(pcm, sampleRate, channels)
	default:
		return nil, errors.New("unsupported target format")
	}
}

// convertChannels converts between mono and stereo PCM with buffer pooling
func convertChannels(pcm []byte, fromChannels, toChannels int) ([]byte, error) {
	if fromChannels == toChannels {
		return pcm, nil
	}
	if fromChannels == 1 && toChannels == 2 {
		return monoToStereo(pcm), nil
	}
	if fromChannels == 2 && toChannels == 1 {
		return stereoToMono(pcm), nil
	}
	return nil, fmt.Errorf("unsupported channel conversion: %d to %d", fromChannels, toChannels)
}

// monoToStereo converts mono PCM to stereo by duplicating channels with buffer pooling
func monoToStereo(monoPCM []byte) []byte {
	samples := len(monoPCM) / 2
	resultSize := samples * 4

	// Get buffer from pool
	result := getChannelConvBuffer(resultSize)
	defer putChannelConvBuffer(result)

	// Ensure result has sufficient capacity
	if cap(result) < resultSize {
		result = make([]byte, resultSize)
	} else {
		result = result[:resultSize]
	}

	for i := 0; i < samples; i++ {
		// Copy left channel
		result[i*4] = monoPCM[i*2]
		result[i*4+1] = monoPCM[i*2+1]
		// Copy right channel (same as left)
		result[i*4+2] = monoPCM[i*2]
		result[i*4+3] = monoPCM[i*2+1]
	}

	// Make a copy to return (can't return pooled buffer directly)
	finalResult := make([]byte, resultSize)
	copy(finalResult, result)
	return finalResult
}

// stereoToMono converts stereo PCM to mono by averaging channels with buffer pooling
func stereoToMono(stereoPCM []byte) []byte {
	samples := len(stereoPCM) / 4
	resultSize := samples * 2

	// Get buffer from pool
	result := getChannelConvBuffer(resultSize)
	defer putChannelConvBuffer(result)

	// Ensure result has sufficient capacity
	if cap(result) < resultSize {
		result = make([]byte, resultSize)
	} else {
		result = result[:resultSize]
	}

	// Get uint16 buffer from pool
	uint16Buf := getUint16Buffer()
	defer putUint16Buffer(uint16Buf)

	for i := range samples {
		// Read left and right samples
		left := int16(binary.LittleEndian.Uint16(stereoPCM[i*4 : i*4+2]))
		right := int16(binary.LittleEndian.Uint16(stereoPCM[i*4+2 : i*4+4]))

		// Average the channels
		mono := (int(left) + int(right)) / 2

		// Write mono sample using pooled buffer
		binary.LittleEndian.PutUint16(uint16Buf, uint16(mono))
		result[i*2] = uint16Buf[0]
		result[i*2+1] = uint16Buf[1]
	}

	// Make a copy to return (can't return pooled buffer directly)
	finalResult := make([]byte, resultSize)
	copy(finalResult, result)
	return finalResult
}
