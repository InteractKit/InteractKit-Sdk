package core

import "time"

type AudioEncodingFormat int

const (
	PCM  AudioEncodingFormat = iota // Pulse-code modulation format.
	ULAW                            // μ-law encoding format.
	ALAW                            // A-law encoding format.
	OPUS                            // Opus encoding format.
)

type AudioChunk struct {
	Data       *[]byte             // Raw audio data.
	SampleRate int                 // Sample rate of the audio data.
	Channels   int                 // Number of audio channels.
	Format     AudioEncodingFormat // Encoding format of the audio data.
	Timestamp  time.Time           // Timestamp of the audio chunk.
}

func (ac *AudioChunk) GetDurationInSeconds() float64 {
	if ac.SampleRate == 0 || ac.Channels == 0 {
		return 0.0
	}
	bytesPerSample := 2 // Assuming 16-bit audio (2 bytes per sample)
	totalSamples := len(*ac.Data) / (bytesPerSample * ac.Channels)
	return float64(totalSamples) / float64(ac.SampleRate)
}

type VideoFormat string

const (
	VideoFormatMP4 VideoFormat = "mp4"
	VideoFormatAVI VideoFormat = "avi"
	VideoFormatMKV VideoFormat = "mkv"
)

type VideoChunk struct {
	Data       *[]byte     // Raw video data.
	FrameRate  int         // Frame rate of the video data.
	Resolution string      // Resolution of the video data (e.g., "1920x1080").
	Format     VideoFormat // Format of the video data.
	Timestamp  time.Time   // Timestamp of the video chunk.
}

type TextChunk struct {
	Text string // Text data.
}

type MediaChunk struct {
	Audio AudioChunk // Optional audio chunk.
	Video VideoChunk // Optional video chunk.
	Text  TextChunk  // Optional text chunk.
}
