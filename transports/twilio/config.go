package twilio

// Config holds the configuration for Twilio WebSocket services
type Config struct {
	// WebSocket server port
	Port int `yaml:"port" json:"port" default:"8080"`

	// WebSocket endpoint path
	Path string `yaml:"path" json:"path" default:"/media-stream"`

	// Enable authentication
	EnableAuth bool `yaml:"enable_auth" json:"enable_auth" default:"false"`

	// Authentication token for webhook validation
	AuthToken string `yaml:"auth_token" json:"auth_token"`

	// Read buffer size for WebSocket connections (bytes)
	ReadBufferSize int `yaml:"read_buffer_size" json:"read_buffer_size" default:"4096"`

	// Write buffer size for WebSocket connections (bytes)
	WriteBufferSize int `yaml:"write_buffer_size" json:"write_buffer_size" default:"4096"`

	// Maximum message size (bytes)
	MaxMessageSize int64 `yaml:"max_message_size" json:"max_message_size" default:"65536"`

	// Audio sample rate (Hz)
	AudioSampleRate int `yaml:"audio_sample_rate" json:"audio_sample_rate" default:"8000"`

	// Enable TLS/SSL
	EnableTLS bool `yaml:"enable_tls" json:"enable_tls" default:"false"`

	// TLS certificate file path
	TLSCertFile string `yaml:"tls_cert_file" json:"tls_cert_file"`

	// TLS key file path
	TLSKeyFile string `yaml:"tls_key_file" json:"tls_key_file"`
}

// DefaultConfig returns a configuration with default values
func DefaultConfig() *Config {
	return &Config{
		Port:            8080,
		Path:            "/media-stream",
		EnableAuth:      false,
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		MaxMessageSize:  65536,
		AudioSampleRate: 8000,
		EnableTLS:       false,
	}
}
