package stt

import "interactkit/core"

type STTConfig struct {
	RequiredSampleRate  int // The required sample rate for the audio input, in Hz. This ensures that the STT engine receives audio data at the optimal quality for accurate transcription.
	RequiredChannels    int
	RequiredAudioFormat core.AudioEncodingFormat // The required audio encoding format for the input audio data. This ensures that the STT engine can correctly interpret the audio data for transcription.
}
