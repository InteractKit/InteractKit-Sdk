package tts

import "interactkit/core"

type TTSOutputEvent struct {
	AudioChunk core.AudioChunk
}

func (e *TTSOutputEvent) GetId() string {
	return "tts.output"
}

type TTSSpokenTextChunkEvent struct {
	Text string
}

func (e *TTSSpokenTextChunkEvent) GetId() string {
	return "tts.spoken_text_chunk"
}

// started and ended events
type TTSSpeakingStartedEvent struct{}

func (e *TTSSpeakingStartedEvent) GetId() string {
	return "tts.speaking_started"
}

type TTSSpeakingEndedEvent struct{}

func (e *TTSSpeakingEndedEvent) GetId() string {
	return "tts.speaking_ended"
}

// TTSSpeakEvent triggers the TTS to immediately speak the given text,
// bypassing the normal LLM chunk accumulation pipeline.
type TTSSpeakEvent struct {
	Text string
}

func (e *TTSSpeakEvent) GetId() string {
	return "tts.speak"
}
