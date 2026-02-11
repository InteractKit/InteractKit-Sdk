package stt

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gorilla/websocket"
)

// DeepgramSTTService implements the ISTTService interface for Deepgram's streaming STT
type DeepgramSTTService struct {
	apiKey  string
	baseURL string
	config  *DeepgramConfig

	conn        *websocket.Conn
	connMu      sync.RWMutex
	isConnected bool

	outChan               chan<- string
	interimOutputChan     chan<- string
	fatalServiceErrorChan chan<- error

	closeOnce   sync.Once
	done        chan struct{}
	reconnectMu sync.Mutex
}

// DeepgramConfig holds configuration options for Deepgram STT
type DeepgramConfig struct {
	Model           string
	Language        string
	InterimResults  bool
	Punctuate       bool
	SmartFormat     bool
	Diarize         bool
	ProfanityFilter bool
	Redact          string
	Numerals        bool
	Endpointing     any // Can be string, int, or bool
	VadEvents       bool
	UtteranceEndMs  any
	Encoding        string
	SampleRate      int
	Channels        int
	Multichannel    bool
	Keywords        []string
	Keyterms        []string
	Search          []string
	Replace         any
	Callback        string
	CallbackMethod  string
	Extra           map[string]string
	Tag             []string
	DetectEntities  bool
	Dictation       bool
	MipOptOut       bool
	Version         string
}

// DefaultConfig returns a default configuration for Deepgram
func DefaultConfig() *DeepgramConfig {
	return &DeepgramConfig{
		Model:          "nova-2-general",
		InterimResults: true,
		Punctuate:      true,
		SmartFormat:    true,
		Encoding:       "linear16",
		SampleRate:     16000,
		Endpointing:    10,
	}
}

// NewDeepgramSTTService creates a new Deepgram STT service instance
func NewDeepgramSTTService(apiKey string, config *DeepgramConfig) *DeepgramSTTService {
	if config == nil {
		config = DefaultConfig()
	}

	return &DeepgramSTTService{
		apiKey:  apiKey,
		baseURL: "wss://api.deepgram.com",
		config:  config,
		done:    make(chan struct{}),
	}
}

// Init initializes the Deepgram STT service
func (d *DeepgramSTTService) Init(ctx context.Context) error {
	// Validate configuration
	if d.apiKey == "" {
		return fmt.Errorf("Deepgram API key is required")
	}

	return nil
}

// Cleanup cleans up resources used by the service
func (d *DeepgramSTTService) Cleanup() error {
	d.closeOnce.Do(func() {
		close(d.done)
		d.closeConnection()
	})
	return nil
}

// Reset resets the service to its initial state
func (d *DeepgramSTTService) Reset() error {
	// Close existing connection
	d.closeConnection()

	// Reset state
	d.conn = nil
	d.isConnected = false

	return nil
}

// StartTranscriptionSession starts a new transcription session with Deepgram
func (d *DeepgramSTTService) StartTranscriptionSession(
	outChan chan<- string,
	interimOutputChan chan<- string,
	fatalServiceErrorChan chan<- error,
) {
	d.outChan = outChan
	d.interimOutputChan = interimOutputChan
	d.fatalServiceErrorChan = fatalServiceErrorChan

	go d.runSession()
}

// SendTranscriptionAudio sends audio data to the active transcription session
func (d *DeepgramSTTService) SendTranscriptionAudio(audioData []byte) error {
	d.connMu.RLock()
	defer d.connMu.RUnlock()

	if !d.isConnected || d.conn == nil {
		return fmt.Errorf("not connected to Deepgram")
	}

	// Send audio data as binary message
	err := d.conn.WriteMessage(websocket.BinaryMessage, audioData)
	if err != nil {
		// Attempt to reconnect on write error
		go d.handleConnectionError(err)
		return fmt.Errorf("failed to send audio: %w", err)
	}

	return nil
}

// runSession manages the WebSocket connection and message handling
func (d *DeepgramSTTService) runSession() {
	for {
		select {
		case <-d.done:
			return
		default:
			if err := d.connectAndListen(); err != nil {
				select {
				case d.fatalServiceErrorChan <- fmt.Errorf("Deepgram session error: %w", err):
				case <-d.done:
					return
				default:
					// Non-blocking send
				}

				// Wait before reconnecting
				select {
				case <-time.After(5 * time.Second):
				case <-d.done:
					return
				}
			}
		}
	}
}

// connectAndListen establishes connection and listens for messages
func (d *DeepgramSTTService) connectAndListen() error {
	d.reconnectMu.Lock()
	defer d.reconnectMu.Unlock()

	// Build WebSocket URL with query parameters
	wsURL, err := d.buildWebSocketURL()
	if err != nil {
		return fmt.Errorf("failed to build WebSocket URL: %w", err)
	}

	// Set up headers
	headers := map[string][]string{
		"Authorization": {"Token " + d.apiKey},
	}

	// Establish connection
	conn, _, err := websocket.DefaultDialer.DialContext(context.Background(), wsURL, headers)
	if err != nil {
		return fmt.Errorf("failed to connect to Deepgram: %w", err)
	}

	d.connMu.Lock()
	d.conn = conn
	d.isConnected = true
	d.connMu.Unlock()

	defer d.closeConnection()

	// Send initial keep-alive message
	go d.keepAlive()

	// Listen for messages
	for {
		select {
		case <-d.done:
			return nil
		default:
			messageType, message, err := conn.ReadMessage()
			if err != nil {
				return fmt.Errorf("error reading message: %w", err)
			}

			if messageType == websocket.TextMessage {
				if err := d.handleMessage(message); err != nil {
					// Log error but continue processing
					_ = err
				}
			}
		}
	}
}

// buildWebSocketURL constructs the WebSocket URL with query parameters
func (d *DeepgramSTTService) buildWebSocketURL() (string, error) {
	base, err := url.Parse(d.baseURL + "/v1/listen")
	if err != nil {
		return "", err
	}

	q := base.Query()

	// Add all configuration parameters
	if d.config.Model != "" {
		q.Set("model", d.config.Model)
	}
	if d.config.Language != "" {
		q.Set("language", d.config.Language)
	}
	q.Set("interim_results", boolToString(d.config.InterimResults))
	q.Set("punctuate", boolToString(d.config.Punctuate))
	q.Set("smart_format", boolToString(d.config.SmartFormat))
	q.Set("diarize", boolToString(d.config.Diarize))
	q.Set("profanity_filter", boolToString(d.config.ProfanityFilter))
	q.Set("numerals", boolToString(d.config.Numerals))
	q.Set("vad_events", boolToString(d.config.VadEvents))
	q.Set("multichannel", boolToString(d.config.Multichannel))
	q.Set("detect_entities", boolToString(d.config.DetectEntities))
	q.Set("dictation", boolToString(d.config.Dictation))
	q.Set("mip_opt_out", boolToString(d.config.MipOptOut))

	if d.config.Redact != "" {
		q.Set("redact", d.config.Redact)
	}

	if d.config.Encoding != "" {
		q.Set("encoding", d.config.Encoding)
	}

	if d.config.SampleRate > 0 {
		q.Set("sample_rate", intToString(d.config.SampleRate))
	}

	if d.config.Channels > 0 {
		q.Set("channels", intToString(d.config.Channels))
	}

	if d.config.Endpointing != nil {
		switch v := d.config.Endpointing.(type) {
		case int:
			q.Set("endpointing", intToString(v))
		case bool:
			q.Set("endpointing", boolToString(v))
		case string:
			q.Set("endpointing", v)
		}
	}

	if d.config.UtteranceEndMs != nil {
		switch v := d.config.UtteranceEndMs.(type) {
		case int:
			q.Set("utterance_end_ms", intToString(v))
		case string:
			q.Set("utterance_end_ms", v)
		}
	}

	// Handle array parameters
	for _, keyword := range d.config.Keywords {
		q.Add("keywords", keyword)
	}

	for _, keyterm := range d.config.Keyterms {
		q.Add("keyterm", keyterm)
	}

	for _, search := range d.config.Search {
		q.Add("search", search)
	}

	for _, tag := range d.config.Tag {
		q.Add("tag", tag)
	}

	if d.config.Callback != "" {
		q.Set("callback", d.config.Callback)
	}

	if d.config.CallbackMethod != "" {
		q.Set("callback_method", d.config.CallbackMethod)
	}

	if d.config.Version != "" {
		q.Set("version", d.config.Version)
	}

	// Handle extra parameters
	for key, value := range d.config.Extra {
		q.Set(key, value)
	}

	base.RawQuery = q.Encode()
	return base.String(), nil
}

// handleMessage processes incoming WebSocket messages
func (d *DeepgramSTTService) handleMessage(message []byte) error {
	var base struct {
		Type string `sonic:"type"`
	}

	if err := sonic.Unmarshal(message, &base); err != nil {
		return fmt.Errorf("failed to parse message type: %w", err)
	}

	switch base.Type {
	case "Results":
		var result ListenV1Results
		if err := sonic.Unmarshal(message, &result); err != nil {
			return fmt.Errorf("failed to parse results: %w", err)
		}
		d.processResults(result)

	case "Metadata":
		var metadata ListenV1Metadata
		if err := sonic.Unmarshal(message, &metadata); err != nil {
			return fmt.Errorf("failed to parse metadata: %w", err)
		}
		// Handle metadata if needed

	case "UtteranceEnd":
		var utteranceEnd ListenV1UtteranceEnd
		if err := sonic.Unmarshal(message, &utteranceEnd); err != nil {
			return fmt.Errorf("failed to parse utterance end: %w", err)
		}
		// Handle utterance end if needed

	case "SpeechStarted":
		var speechStarted ListenV1SpeechStarted
		if err := sonic.Unmarshal(message, &speechStarted); err != nil {
			return fmt.Errorf("failed to parse speech started: %w", err)
		}
		// Handle speech started if needed

	default:
		return fmt.Errorf("unknown message type: %s", base.Type)
	}

	return nil
}

// processResults handles transcription results
func (d *DeepgramSTTService) processResults(result ListenV1Results) {
	if len(result.Channel.Alternatives) == 0 {
		return
	}

	transcript := result.Channel.Alternatives[0].Transcript

	if result.IsFinal {
		select {
		case d.outChan <- transcript:
		case <-d.done:
		default:
			// Non-blocking send
		}
	} else {
		select {
		case d.interimOutputChan <- transcript:
		case <-d.done:
		default:
			// Non-blocking send
		}
	}
}

// keepAlive sends periodic keep-alive messages
func (d *DeepgramSTTService) keepAlive() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			d.connMu.RLock()
			if d.isConnected && d.conn != nil {
				keepAliveMsg := ListenV1KeepAlive{Type: "KeepAlive"}
				if msg, err := sonic.Marshal(keepAliveMsg); err == nil {
					_ = d.conn.WriteMessage(websocket.TextMessage, msg)
				}
			}
			d.connMu.RUnlock()
		}
	}
}

// closeConnection safely closes the WebSocket connection
func (d *DeepgramSTTService) closeConnection() {
	d.connMu.Lock()
	defer d.connMu.Unlock()

	if d.conn != nil {
		// Send close message
		closeMsg := ListenV1CloseStream{Type: "CloseStream"}
		if msg, err := sonic.Marshal(closeMsg); err == nil {
			_ = d.conn.WriteMessage(websocket.TextMessage, msg)
		}

		_ = d.conn.Close()
		d.conn = nil
	}

	d.isConnected = false
}

// handleConnectionError handles connection errors and triggers reconnection
func (d *DeepgramSTTService) handleConnectionError(_ error) {
	d.connMu.Lock()
	d.isConnected = false
	d.connMu.Unlock()

	// Error will be caught in runSession which will trigger reconnection
}

// Helper functions
func boolToString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func intToString(i int) string {
	return fmt.Sprintf("%d", i)
}

// Message structs based on the AsyncAPI specification

type ListenV1Results struct {
	Type         string  `sonic:"type"`
	ChannelIndex []int   `sonic:"channel_index"`
	Duration     float64 `sonic:"duration"`
	Start        float64 `sonic:"start"`
	IsFinal      bool    `sonic:"is_final"`
	SpeechFinal  bool    `sonic:"speech_final"`
	Channel      struct {
		Alternatives []struct {
			Transcript string  `sonic:"transcript"`
			Confidence float64 `sonic:"confidence"`
			Words      []struct {
				Word           string  `sonic:"word"`
				Start          float64 `sonic:"start"`
				End            float64 `sonic:"end"`
				Confidence     float64 `sonic:"confidence"`
				Speaker        int     `sonic:"speaker,omitempty"`
				PunctuatedWord string  `sonic:"punctuated_word,omitempty"`
			} `sonic:"words"`
		} `sonic:"alternatives"`
	} `sonic:"channel"`
	Metadata struct {
		RequestID string `sonic:"request_id"`
		ModelInfo struct {
			Name    string `sonic:"name"`
			Version string `sonic:"version"`
			Arch    string `sonic:"arch"`
		} `sonic:"model_info"`
		ModelUUID string `sonic:"model_uuid"`
	} `sonic:"metadata"`
	FromFinalize bool `sonic:"from_finalize,omitempty"`
	Entities     []struct {
		Label      string  `sonic:"label"`
		Value      string  `sonic:"value"`
		RawValue   string  `sonic:"raw_value"`
		Confidence float64 `sonic:"confidence"`
		StartWord  int     `sonic:"start_word"`
		EndWord    int     `sonic:"end_word"`
	} `sonic:"entities,omitempty"`
}

type ListenV1Metadata struct {
	Type           string  `sonic:"type"`
	TransactionKey string  `sonic:"transaction_key"`
	RequestID      string  `sonic:"request_id"`
	Sha256         string  `sonic:"sha256"`
	Created        string  `sonic:"created"`
	Duration       float64 `sonic:"duration"`
	Channels       int     `sonic:"channels"`
}

type ListenV1UtteranceEnd struct {
	Type        string  `sonic:"type"`
	Channel     []int   `sonic:"channel"`
	LastWordEnd float64 `sonic:"last_word_end"`
}

type ListenV1SpeechStarted struct {
	Type      string  `sonic:"type"`
	Channel   []int   `sonic:"channel"`
	Timestamp float64 `sonic:"timestamp"`
}

type ListenV1KeepAlive struct {
	Type string `sonic:"type"`
}

type ListenV1CloseStream struct {
	Type string `sonic:"type"`
}

type ListenV1Finalize struct {
	Type string `sonic:"type"`
}
