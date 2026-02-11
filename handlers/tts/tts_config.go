package tts

type TTSConfig struct {
	ContextSize int // Context size for TTS processing, e.g., number of previous tokens kept in memory for generating the next token.
}
