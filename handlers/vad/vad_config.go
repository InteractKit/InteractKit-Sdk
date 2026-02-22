package vad

type VADConfig struct {
	MinConfidence                     float32 `json:"min_confidence"`                        // Minimum confidence threshold for voice activity detection. Values range from 0.0 to 1.0, where higher values indicate stricter detection.
	AllowInterruptions                bool    `json:"allow_interruptions"`                   // If true, interruptions from the user while the bot is speaking are detected and handled.
	VadPatienceIncreaseOnInterruption float32 `json:"vad_patience_increase_on_interruption"` // The amount of time (in seconds) to increase the VAD grace period when an interruption is detected.
}

// DefaultConfig returns a VADConfig with sensible defaults
func DefaultConfig() VADConfig {
	return VADConfig{
		MinConfidence:                     0.5,
		AllowInterruptions:                true,
		VadPatienceIncreaseOnInterruption: 0.2,
	}
}
