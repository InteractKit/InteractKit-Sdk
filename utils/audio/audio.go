package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"interactkit/core"

	"github.com/zaf/g711"
)

// PCM constants
const (
	pcmMax = 32767  // Max 16-bit PCM value
	pcmMin = -32768 // Min 16-bit PCM value
)

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

// PCMBytesToWavBytes wraps PCM []byte into WAV []byte (16-bit little endian)
// Supports mono or stereo
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

	var buf bytes.Buffer

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
	binary.Write(&buf, binary.LittleEndian, uint32(fileSize))
	buf.WriteString("WAVE")

	// Write fmt sub-chunk
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(subchunk1Size))
	binary.Write(&buf, binary.LittleEndian, uint16(audioFormatPCM))
	binary.Write(&buf, binary.LittleEndian, uint16(numChannels))
	binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))
	binary.Write(&buf, binary.LittleEndian, uint32(byteRate))
	binary.Write(&buf, binary.LittleEndian, uint16(blockAlign))
	binary.Write(&buf, binary.LittleEndian, uint16(bitsPerSample))

	// Write data sub-chunk
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(dataSize))
	buf.Write(pcm)

	return buf.Bytes(), nil
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

func ConvertAudioChunk(
	input core.AudioChunk,
	targetFormat core.AudioEncodingFormat,
	targetChannels int,
	targetSampleRate int,
) (core.AudioChunk, error) {
	needToConvertFormat := input.Format != targetFormat
	needToConvertSampleRate := input.SampleRate != targetSampleRate
	if !needToConvertFormat && !needToConvertSampleRate {
		return input, nil
	}

	if needToConvertSampleRate {
		// convert to PCM if not already
		if input.Format != core.PCM {
			switch input.Format {
			case core.ULAW:
				pcmBytes := ULawBytesToPCM(*input.Data)
				input.Data = &pcmBytes
			case core.ALAW:
				pcmBytes := ALawBytesToPCM(*input.Data)
				input.Data = &pcmBytes
			default:
				return core.AudioChunk{}, errors.New("unsupported format for sample rate conversion")
			}
			input.Format = core.PCM
		}

		// convert sample rate
		resampledBytes, err := ResamplePCMBytes(*input.Data, input.SampleRate, targetSampleRate, input.Channels)
		if err != nil {
			return core.AudioChunk{}, err
		}
		input.Data = &resampledBytes
		input.SampleRate = targetSampleRate
	}

	if needToConvertFormat {
		switch input.Format {
		case core.PCM:
			switch targetFormat {
			case core.ULAW:
				ulawBytes, err := PCMBytesToULaw(*input.Data)
				if err != nil {
					return core.AudioChunk{}, err
				}
				input.Data = &ulawBytes
			case core.ALAW:
				alawBytes, err := PCMBytesToALaw(*input.Data)
				if err != nil {
					return core.AudioChunk{}, err
				}
				input.Data = &alawBytes
			default:
				return core.AudioChunk{}, errors.New("unsupported target format")
			}
		case core.ULAW:
			switch targetFormat {
			case core.PCM:
				pcmBytes := ULawBytesToPCM(*input.Data)
				input.Data = &pcmBytes
			case core.ALAW:
				pcmBytes := ULawBytesToPCM(*input.Data)
				alawBytes, err := PCMBytesToALaw(pcmBytes)
				if err != nil {
					return core.AudioChunk{}, err
				}
				input.Data = &alawBytes
			default:
				return core.AudioChunk{}, errors.New("unsupported target format conversion from ULAW")
			}
		case core.ALAW:
			switch targetFormat {
			case core.PCM:
				pcmBytes := ALawBytesToPCM(*input.Data)
				input.Data = &pcmBytes
			case core.ULAW:
				pcmBytes := ALawBytesToPCM(*input.Data)
				ulawBytes, err := PCMBytesToULaw(pcmBytes)
				if err != nil {
					return core.AudioChunk{}, err
				}
				input.Data = &ulawBytes
			default:
				return core.AudioChunk{}, errors.New("unsupported target format conversion from ALAW")
			}
		default:
			return core.AudioChunk{}, errors.New("unsupported input format")
		}
		input.Format = targetFormat
	}

	return input, nil
}
