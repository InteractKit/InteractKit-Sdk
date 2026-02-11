package transport

import "interactkit/core"

type SerializerService interface {
	Deserialize(core.RawData) (core.AudioChunk, core.VideoChunk, string, error)
	SerializeAudioOutput(audioChunk core.AudioChunk) (core.RawData, error)
	SerializeVideoOutput(videoChunk core.VideoChunk) (core.RawData, error)
	SerializeTextOutput(text string) (core.RawData, error)
}

type TransportConfig struct {
	serializer     SerializerService
	OutSampleRate  int
	OutChannels    int
	OutAudioFormat core.AudioEncodingFormat
}
