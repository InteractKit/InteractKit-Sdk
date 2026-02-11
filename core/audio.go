package core

type AudioEncodingFormat int

const (
	PCM  AudioEncodingFormat = iota // Pulse-code modulation format.
	ULAW                            // μ-law encoding format.
	ALAW                            // A-law encoding format.
	// AAC                             // Advanced Audio Coding format. NOT SUPPORTED YET.
	// MP3                             // MPEG-1 Audio Layer III format. NOT SUPPORTED YET.
	// FLAC                            // Free Lossless Audio Codec format. NOT SUPPORTED YET.
)

type AudioChunk struct {
	Data       *[]byte             // Raw audio data.
	SampleRate int                 // Sample rate of the audio data.
	Channels   int                 // Number of audio channels.
	Format     AudioEncodingFormat // Encoding format of the audio data.
}

func (ac *AudioChunk) GetDurationInSeconds() float64 {
	if ac.SampleRate == 0 || ac.Channels == 0 {
		return 0.0
	}
	bytesPerSample := 2 // Assuming 16-bit audio (2 bytes per sample)
	totalSamples := len(*ac.Data) / (bytesPerSample * ac.Channels)
	return float64(totalSamples) / float64(ac.SampleRate)
}
