package factories

import (
	"encoding/json"
	"errors"
	"fmt"
	"interactkit/core"
	"interactkit/handlers/transport"
	"interactkit/transports/daily"
	"interactkit/transports/livekit"
	"os"
	"time"
)

// LiveKitProviderConfig holds JSON-serialisable settings for a LiveKit transport provider.
// Secrets (URL, APIKey, APISecret) can be omitted and injected via InjectProviderKeys.
type LiveKitProviderConfig struct {
	URL                 string `json:"url,omitempty"`
	APIKey              string `json:"api_key,omitempty"`
	APISecret           string `json:"api_secret,omitempty"`
	AgentName           string `json:"agent_name,omitempty"`
	Version             string `json:"version,omitempty"`
	MaxJobs             uint32 `json:"max_jobs,omitempty"`
	DevMode             bool   `json:"dev_mode"`
	HTTPPort            int    `json:"http_port,omitempty"`
	DrainTimeoutSeconds int    `json:"drain_timeout_seconds,omitempty"`
}

// DailyProviderConfig holds JSON-serialisable settings for a Daily.co transport provider.
// Secrets (APIKey) can be omitted and injected via InjectProviderKeys.
type DailyProviderConfig struct {
	APIKey          string `json:"api_key,omitempty"`
	APIBaseURL      string `json:"api_base_url,omitempty"`
	RoomName        string `json:"room_name,omitempty"`
	RoomURLPrefix   string `json:"room_url_prefix,omitempty"`
	ExpirySeconds   int    `json:"expiry_seconds,omitempty"`
	MaxParticipants int    `json:"max_participants,omitempty"`
	Port            int    `json:"port,omitempty"`
	Path            string `json:"path,omitempty"`
	AudioSampleRate int    `json:"audio_sample_rate,omitempty"`
	AudioChannels   int    `json:"audio_channels,omitempty"`
	BotName         string `json:"bot_name,omitempty"`
	IsOwner         bool   `json:"is_owner,omitempty"`
}

// TransportFactoryConfig selects and configures a transport provider.
// Set exactly one provider field.
type TransportFactoryConfig struct {
	LiveKitConfig *LiveKitProviderConfig `json:"livekit,omitempty"`
	DailyConfig   *DailyProviderConfig   `json:"daily,omitempty"`
}

// ProviderKeys holds credentials for transport providers.
// Pass to TransportFactoryConfig.InjectProviderKeys after loading from JSON
// so that secrets are not stored in config files.
type ProviderKeys struct {
	LiveKitURL       string
	LiveKitAPIKey    string
	LiveKitAPISecret string
	DailyAPIKey      string
}

// DefaultTransportFactoryConfig returns a TransportFactoryConfig pre-filled with
// LiveKit provider defaults. Populate credential fields before calling BuildLiveKitProvider.
func DefaultTransportFactoryConfig() TransportFactoryConfig {
	base := livekit.DefaultConfig()
	return TransportFactoryConfig{
		LiveKitConfig: &LiveKitProviderConfig{
			AgentName: base.AgentName,
			Version:   base.Version,
			MaxJobs:   base.MaxJobs,
			HTTPPort:  base.HTTPPort,
		},
	}
}

// TransportFactoryConfigFromJSON parses a JSON blob into a TransportFactoryConfig.
// It detects which provider key is present in the JSON and only populates that
// provider's config (with defaults), so the other remains nil and GetProvider
// selects the correct one.
func TransportFactoryConfigFromJSON(data []byte) (TransportFactoryConfig, error) {
	// Peek at the raw keys to determine which provider is configured.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return TransportFactoryConfig{}, fmt.Errorf("transport factory config: %w", err)
	}

	var cfg TransportFactoryConfig
	if _, ok := raw["daily"]; ok {
		// Daily is explicitly configured — start with Daily defaults only.
		cfg.DailyConfig = &DailyProviderConfig{}
	} else {
		// Default to LiveKit.
		cfg = DefaultTransportFactoryConfig()
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return TransportFactoryConfig{}, fmt.Errorf("transport factory config: %w", err)
	}
	return cfg, nil
}

// TransportFactoryConfigFromFile reads and parses a TransportFactoryConfig from a JSON file.
// Returns an error (and the defaults) if the file cannot be read or parsed.
func TransportFactoryConfigFromFile(path string) (TransportFactoryConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultTransportFactoryConfig(), fmt.Errorf("transport factory config: read %q: %w", path, err)
	}
	return TransportFactoryConfigFromJSON(data)
}

// InjectProviderKeys applies credentials to the config only when the existing value is empty,
// so keys already set in the config file are preserved.
func (c *TransportFactoryConfig) InjectProviderKeys(keys ProviderKeys) {
	if c.LiveKitConfig != nil {
		lk := c.LiveKitConfig
		if lk.URL == "" {
			lk.URL = keys.LiveKitURL
		}
		if lk.APIKey == "" {
			lk.APIKey = keys.LiveKitAPIKey
		}
		if lk.APISecret == "" {
			lk.APISecret = keys.LiveKitAPISecret
		}
	}
	if c.DailyConfig != nil {
		if c.DailyConfig.APIKey == "" {
			c.DailyConfig.APIKey = keys.DailyAPIKey
		}
	}
}

// GetProvider constructs the transport provider selected by this config.
// It inspects which provider config is set and builds the appropriate implementation.
func (c TransportFactoryConfig) GetProvider(logger *core.Logger) (transport.ITransportProvider, error) {
	if c.LiveKitConfig != nil {
		return c.buildLiveKitProvider(logger)
	}
	if c.DailyConfig != nil {
		return c.buildDailyProvider(logger)
	}
	return nil, errors.New("TransportFactoryConfig: no provider config specified")
}

// buildLiveKitProvider constructs a livekit.Provider from the config.
func (c TransportFactoryConfig) buildLiveKitProvider(logger *core.Logger) (*livekit.Provider, error) {
	if c.LiveKitConfig == nil {
		return nil, errors.New("TransportFactoryConfig: no provider config specified")
	}
	cfg := livekit.DefaultConfig()
	lk := c.LiveKitConfig
	if lk.URL != "" {
		cfg.URL = lk.URL
	}
	if lk.APIKey != "" {
		cfg.APIKey = lk.APIKey
	}
	if lk.APISecret != "" {
		cfg.APISecret = lk.APISecret
	}
	if lk.AgentName != "" {
		cfg.AgentName = lk.AgentName
	}
	if lk.Version != "" {
		cfg.Version = lk.Version
	}
	if lk.MaxJobs != 0 {
		cfg.MaxJobs = lk.MaxJobs
	}
	if lk.DevMode {
		cfg.DevMode = true
	}
	if lk.HTTPPort != 0 {
		cfg.HTTPPort = lk.HTTPPort
	}
	if lk.DrainTimeoutSeconds != 0 {
		cfg.DrainTimeout = time.Duration(lk.DrainTimeoutSeconds) * time.Second
	}
	cfg.Logger = logger
	return livekit.NewProvider(cfg)
}

// buildDailyProvider constructs a daily.DailyTransportProvider from the config.
func (c TransportFactoryConfig) buildDailyProvider(logger *core.Logger) (*daily.DailyTransportProvider, error) {
	if c.DailyConfig == nil {
		return nil, errors.New("TransportFactoryConfig: no Daily config specified")
	}
	cfg := daily.DefaultConfig()
	dc := c.DailyConfig
	if dc.APIKey != "" {
		cfg.APIKey = dc.APIKey
	}
	if dc.APIBaseURL != "" {
		cfg.APIBaseURL = dc.APIBaseURL
	}
	if dc.RoomName != "" {
		cfg.RoomName = dc.RoomName
	}
	if dc.RoomURLPrefix != "" {
		cfg.RoomURLPrefix = dc.RoomURLPrefix
	}
	if dc.ExpirySeconds != 0 {
		cfg.ExpirySeconds = dc.ExpirySeconds
	}
	if dc.MaxParticipants != 0 {
		cfg.MaxParticipants = dc.MaxParticipants
	}
	if dc.Port != 0 {
		cfg.Port = dc.Port
	}
	if dc.Path != "" {
		cfg.Path = dc.Path
	}
	if dc.AudioSampleRate != 0 {
		cfg.AudioSampleRate = dc.AudioSampleRate
	}
	if dc.AudioChannels != 0 {
		cfg.AudioChannels = dc.AudioChannels
	}
	if dc.BotName != "" {
		cfg.BotName = dc.BotName
	}
	if dc.IsOwner {
		cfg.IsOwner = true
	}
	return daily.NewDailyTransportProvider(cfg, logger)
}
