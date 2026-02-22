package factories

import (
	"errors"
	"interactkit/core"
	ttshandler "interactkit/handlers/tts"
	cartesia "interactkit/services/cartesia/tts"
	deepgramtts "interactkit/services/deepgram/tts"
	elevenlabs "interactkit/services/elevenlabs/tts"
)

// TTSFactoryConfig holds provider-specific configs for TTS service construction.
// Set exactly one provider config; the rest should be left nil.
type TTSFactoryConfig struct {
	DeepgramConfig   *deepgramtts.DepgramTTSConfig   `json:"deepgram,omitempty"`
	ElevenLabsConfig *elevenlabs.ElevenLabsTTSConfig `json:"elevenlabs,omitempty"`
	CartesiaConfig   *cartesia.CartesiaTTSConfig     `json:"cartesia,omitempty"`
}

// BuildTTSService constructs a TTSService from the given factory config.
// Exactly one provider config must be non-nil.
func BuildTTSService(config TTSFactoryConfig, logger *core.Logger) (ttshandler.TTSService, error) {
	if config.DeepgramConfig != nil {
		return deepgramtts.NewDepgramTTS(*config.DeepgramConfig, logger), nil
	}
	if config.ElevenLabsConfig != nil {
		return elevenlabs.NewElevenLabsTTS(*config.ElevenLabsConfig, logger), nil
	}
	if config.CartesiaConfig != nil {
		return cartesia.NewCartesiaTTS(*config.CartesiaConfig, logger), nil
	}
	return nil, errors.New("TTSFactoryConfig: no provider config specified")
}
