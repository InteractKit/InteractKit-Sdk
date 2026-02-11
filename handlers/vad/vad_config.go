package vad

type VADConfig struct {
	MinConfidence                             float64 // Minimum confidence threshold for voice activity detection. Values range from 0.0 to 1.0, where higher values indicate stricter detection.
	MinSpeechDuration                         float64 // Minimum duration (in seconds) of detected speech segments to be considered valid. This helps filter out short noises or non-speech sounds.
	MinSilenceDuration                        float64 // Minimum duration (in seconds) of silence required to separate speech segments. This helps in accurately segmenting continuous speech.
	IncreaseVadWaitLengthOnInterruption       bool    // If true, increases the wait length for VAD when an interruption is detected, allowing for better handling of brief pauses in speech.
	IncreaseVadWaitLengthOnInterruptionLength float64 // The amount of time (in seconds) to increase the VAD wait length when an interruption is detected. This can help accommodate speakers who take brief pauses while talking.
}
