package tts

type TTSConfig struct {
	ContextSize   int      `json:"context_size"`    // Context size for TTS processing, e.g., number of previous tokens kept in memory for generating the next token.
	BreakWords    []string `json:"break_words"`     // Punctuation and linguistic markers that trigger early flushing of buffered text to TTS.
	MinTextLength int      `json:"min_text_length"` // Minimum text length to trigger TTS generation, useful for filtering out very short inputs that may not be meaningful to speak.
}

// DefaultConfig returns a TTSConfig with sensible defaults.
// BreakWords and MinTextLength defaults are applied by NewTTSHandler when left zero.
func DefaultConfig() TTSConfig {
	return TTSConfig{
		ContextSize:   100,
		MinTextLength: 20,
	}
}
