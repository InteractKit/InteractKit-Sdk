package vad

import "interactkit/core"

type VADUserSpeechChunkEvent struct {
	AudioChunk core.AudioChunk
}

func (e *VADUserSpeechChunkEvent) GetId() string {
	return "vad.user_speech.chunk"
}

type VADSilenceChunkEvent struct {
	AudioChunk core.AudioChunk
}

func (e *VADSilenceChunkEvent) GetId() string {
	return "vad.silence.chunk"
}

type VadInterruptionDetectedEvent struct {
}

func (e *VadInterruptionDetectedEvent) GetId() string {
	return "vad.interruption.detected"
}

type VadUserSpeechEndedEvent struct {
}

func (e *VadUserSpeechEndedEvent) GetId() string {
	return "vad.user_speech.ended"
}

type VadUserSpeechStartedEvent struct {
}

func (e *VadUserSpeechStartedEvent) GetId() string {
	return "vad.user_speech.started"
}
