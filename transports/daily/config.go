package daily

// Config holds configuration for the Daily.co transport.
type Config struct {
	// Daily API key for REST API authentication.
	APIKey string `json:"api_key,omitempty"`

	// Daily API base URL (default: https://api.daily.co/v1).
	APIBaseURL string `json:"api_base_url,omitempty"`

	// Room configuration.
	RoomName        string `json:"room_name,omitempty"`
	RoomURLPrefix   string `json:"room_url_prefix,omitempty"`
	ExpirySeconds   int    `json:"expiry_seconds,omitempty"`
	MaxParticipants int    `json:"max_participants,omitempty"`

	// WebSocket relay server configuration.
	Port            int    `json:"port,omitempty"`
	Path            string `json:"path,omitempty"`
	ReadBufferSize  int    `json:"read_buffer_size,omitempty"`
	WriteBufferSize int    `json:"write_buffer_size,omitempty"`
	MaxMessageSize  int64  `json:"max_message_size,omitempty"`

	// Audio configuration.
	AudioSampleRate int `json:"audio_sample_rate,omitempty"`
	AudioChannels   int `json:"audio_channels,omitempty"`

	// TLS configuration.
	EnableTLS   bool   `json:"enable_tls,omitempty"`
	TLSCertFile string `json:"tls_cert_file,omitempty"`
	TLSKeyFile  string `json:"tls_key_file,omitempty"`

	// Bot participant name in the Daily room.
	BotName string `json:"bot_name,omitempty"`
	// Whether the bot token has owner privileges.
	IsOwner bool `json:"is_owner,omitempty"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		APIBaseURL:      "https://api.daily.co/v1",
		ExpirySeconds:   3600,
		MaxParticipants: 2,
		Port:            8090,
		Path:            "/daily-media",
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		MaxMessageSize:  65536,
		AudioSampleRate: 24000,
		AudioChannels:   1,
		BotName:         "ai-agent",
	}
}
