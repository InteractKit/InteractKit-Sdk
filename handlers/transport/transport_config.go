package transport

import "interactkit/core"

// TransportConfig holds configuration for the transport handler
type TransportConfig struct {
	OutSampleRate  int                      `json:"out_sample_rate"`  // Sample rate for outgoing audio
	OutChannels    int                      `json:"out_channels"`     // Number of channels for outgoing audio
	OutAudioFormat core.AudioEncodingFormat `json:"out_audio_format"` // Encoding format for outgoing audio
}

// DefaultConfig returns a TransportConfig with sensible defaults
func DefaultConfig() TransportConfig {
	return TransportConfig{
		OutSampleRate:  24000,
		OutChannels:    1,
		OutAudioFormat: core.PCM,
	}
}
