package transport

import "interactkit/core"

type TransportAudioInputEvent struct {
	AudioChunk core.AudioChunk
}

func (e *TransportAudioInputEvent) GetId() string {
	return "serializer.audio_input"
}

type TransportVideoInputEvent struct {
	VideoChunk core.VideoChunk
}

func (e *TransportVideoInputEvent) GetId() string {
	return "serializer.video_input"
}

type TransportTextInputEvent struct {
	Text string
}

func (e *TransportTextInputEvent) GetId() string {
	return "serializer.text_input"
}
