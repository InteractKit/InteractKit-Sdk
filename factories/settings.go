package factories

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// SessionAPIConfig describes an HTTP endpoint that returns a SessionConfig JSON payload.
// Called per-job to allow dynamic configuration per call.
type SessionAPIConfig struct {
	// URL is the endpoint to request.
	URL string `json:"url"`
	// Method is the HTTP method. Defaults to "POST" when Body is set, "GET" otherwise.
	Method string `json:"method,omitempty"`
	// Headers are additional HTTP headers to include in the request.
	Headers map[string]string `json:"headers,omitempty"`
	// Body is an optional JSON body to send with the request.
	Body json.RawMessage `json:"body,omitempty"`
}

var sessionAPIClient = &http.Client{Timeout: 10 * time.Second}

// Fetch calls the configured endpoint and parses the response as a SessionConfig.
func (c *SessionAPIConfig) Fetch() (SessionConfig, error) {
	method := c.Method
	if method == "" {
		if len(c.Body) > 0 {
			method = http.MethodPost
		} else {
			method = http.MethodGet
		}
	}

	var bodyReader *bytes.Reader
	if len(c.Body) > 0 {
		bodyReader = bytes.NewReader(c.Body)
	} else {
		bodyReader = bytes.NewReader(nil)
	}

	req, err := http.NewRequest(method, c.URL, bodyReader)
	if err != nil {
		return SessionConfig{}, fmt.Errorf("session api: %w", err)
	}
	if len(c.Body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}

	resp, err := sessionAPIClient.Do(req)
	if err != nil {
		return SessionConfig{}, fmt.Errorf("session api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return SessionConfig{}, fmt.Errorf("session api: unexpected status %d from %s", resp.StatusCode, c.URL)
	}

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return SessionConfig{}, fmt.Errorf("session api: read response: %w", err)
	}

	return SessionConfigFromJSON(buf.Bytes())
}

// SettingsConfig is the top-level config loaded from settings.json.
// It bundles the transport provider config with an optional HTTP endpoint
// for fetching the per-job SessionConfig.
type SettingsConfig struct {
	// Transport selects and configures the transport provider (e.g. LiveKit).
	Transport TransportFactoryConfig `json:"transport"`
	// SessionAPI, when set, is called per-job to fetch the SessionConfig dynamically.
	SessionAPI *SessionAPIConfig `json:"session_api,omitempty"`
	// Session, when set, provides inline session config directly in settings.json.
	Session *SessionConfig `json:"session_config,omitempty"`
}

// DefaultSettingsConfig returns a SettingsConfig pre-filled with provider defaults.
func DefaultSettingsConfig() SettingsConfig {
	return SettingsConfig{
		Transport: DefaultTransportFactoryConfig(),
	}
}

// SettingsConfigFromJSON parses a JSON blob into a SettingsConfig.
// It delegates provider parsing to TransportFactoryConfigFromJSON so that
// the correct provider is detected from the JSON keys.
func SettingsConfigFromJSON(data []byte) (SettingsConfig, error) {
	// First, extract the raw "provider" object so we can parse it properly.
	var raw struct {
		Transport     json.RawMessage   `json:"transport,omitempty"`
		SessionAPI    *SessionAPIConfig  `json:"session_api,omitempty"`
		SessionConfig json.RawMessage    `json:"session_config,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return SettingsConfig{}, fmt.Errorf("settings: %w", err)
	}

	var cfg SettingsConfig
	cfg.SessionAPI = raw.SessionAPI

	if len(raw.SessionConfig) > 0 {
		sc, err := SessionConfigFromJSON(raw.SessionConfig)
		if err != nil {
			return SettingsConfig{}, fmt.Errorf("settings: %w", err)
		}
		cfg.Session = &sc
	}

	if len(raw.Transport) > 0 {
		transport, err := TransportFactoryConfigFromJSON(raw.Transport)
		if err != nil {
			return SettingsConfig{}, fmt.Errorf("settings: %w", err)
		}
		cfg.Transport = transport
	} else {
		cfg.Transport = DefaultTransportFactoryConfig()
	}

	return cfg, nil
}

// SettingsConfigFromFile reads and parses a SettingsConfig from a JSON file.
func SettingsConfigFromFile(path string) (SettingsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultSettingsConfig(), fmt.Errorf("settings: read %q: %w", path, err)
	}
	return SettingsConfigFromJSON(data)
}
