package vad

type VADUserSpeakingEvent struct {
}

func (e *VADUserSpeakingEvent) GetId() string {
	return "vad.user_speaking"
}

type VADSilenceEvent struct {
}

func (e *VADSilenceEvent) GetId() string {
	return "vad.silence"
}

type VadInterruptionDetectedEvent struct {
}

func (e *VadInterruptionDetectedEvent) GetId() string {
	return "vad.interruption.detected"
}

type VadInterruptionSuspectedEvent struct{}

func (e *VadInterruptionSuspectedEvent) GetId() string {
	return "vad.interruption.suspected"
}

type VadInterruptionConfirmedEvent struct{}

func (e *VadInterruptionConfirmedEvent) GetId() string {
	return "vad.interruption.confirmed"
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
