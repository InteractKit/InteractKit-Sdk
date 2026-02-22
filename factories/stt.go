package factories

import (
	"errors"
	"interactkit/core"
	stthandler "interactkit/handlers/stt"
	deepgramstt "interactkit/services/deepgram/stt"
)

// STTFactoryConfig holds provider-specific configs for STT service construction.
// Set exactly one provider config; the rest should be left nil.
type STTFactoryConfig struct {
	DeepgramConfig *deepgramstt.DeepgramConfig `json:"deepgram,omitempty"`
}

// BuildSTTService constructs an ISTTService from the given factory config.
// Exactly one provider config must be non-nil.
func BuildSTTService(config STTFactoryConfig, logger *core.Logger) (stthandler.ISTTService, error) {
	if config.DeepgramConfig != nil {
		return deepgramstt.NewDeepgramSTTService(config.DeepgramConfig, logger), nil
	}
	return nil, errors.New("STTFactoryConfig: no provider config specified")
}
