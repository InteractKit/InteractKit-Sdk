package tts

import "interactkit/core"

type TTSOutputEvent struct {
	AudioChunk core.AudioChunk
}

func (e *TTSOutputEvent) GetId() string {
	return "tts.output"
}
