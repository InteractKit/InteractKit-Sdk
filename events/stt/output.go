package stt

type STTInterimOutputEvent struct {
	Text string
}

func (e *STTInterimOutputEvent) GetId() string {
	return "stt.interim_output"
}

type STTFinalOutputEvent struct {
	Text string
}

func (e *STTFinalOutputEvent) GetId() string {
	return "stt.final_output"
}
