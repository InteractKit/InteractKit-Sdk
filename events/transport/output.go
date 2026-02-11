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

type TransportAudioOutputEvent struct {
	AudioChunk core.AudioChunk
}

func (e *TransportAudioOutputEvent) GetId() string {
	return "serializer.audio_output"
}

type TransportVideoOutputEvent struct {
	VideoChunk core.VideoChunk
}

func (e *TransportVideoOutputEvent) GetId() string {
	return "serializer.video_output"
}

type TransportTextOutputEvent struct {
	Text string
}

func (e *TransportTextOutputEvent) GetId() string {
	return "serializer.text_output"
}
