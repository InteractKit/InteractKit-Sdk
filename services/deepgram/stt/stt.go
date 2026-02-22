package stt

import (
	"context"
	"encoding/json"
	"fmt"
	"interactkit/core"
	"interactkit/utils/audio"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// DeepgramSTTService implements the ISTTService interface for Deepgram's streaming STT
type DeepgramSTTService struct {
	config  *DeepgramConfig
	logger  *core.Logger

	conn        *websocket.Conn
	connMu      sync.RWMutex
	isConnected bool

	outChan               chan<- string
	interimOutputChan     chan<- string
	fatalServiceErrorChan chan<- error

	done        <-chan struct{}
	reconnectMu sync.Mutex
}

// DeepgramConfig holds configuration options for Deepgram STT
type DeepgramConfig struct {
	APIKey          string            `json:"api_key"`
	BaseURL         string            `json:"base_url"`
	Model           string            `json:"model"`
	Language        string            `json:"language"`
	InterimResults  bool              `json:"interim_results"`
	Punctuate       bool              `json:"punctuate"`
	SmartFormat     bool              `json:"smart_format"`
	Diarize         bool              `json:"diarize"`
	ProfanityFilter bool              `json:"profanity_filter"`
	Redact          string            `json:"redact"`
	Numerals        bool              `json:"numerals"`
	Endpointing     any               `json:"endpointing"` // Can be string, int, or bool
	VadEvents       bool              `json:"vad_events"`
	UtteranceEndMs  any               `json:"utterance_end_ms"`
	Multichannel    bool              `json:"multichannel"`
	Keywords        []string          `json:"keywords"`
	Keyterms        []string          `json:"keyterms"`
	Search          []string          `json:"search"`
	Replace         any               `json:"replace"`
	Callback        string            `json:"callback"`
	CallbackMethod  string            `json:"callback_method"`
	Extra           map[string]string `json:"extra"`
	Tag             []string          `json:"tag"`
	DetectEntities  bool              `json:"detect_entities"`
	Dictation       bool              `json:"dictation"`
	MipOptOut       bool              `json:"mip_opt_out"`
	Version         string            `json:"version"`
}

// DefaultConfig returns a default configuration for Deepgram STT
func DefaultConfig() *DeepgramConfig {
	return &DeepgramConfig{
		BaseURL:        "wss://api.deepgram.com",
		Model:          "nova-2",
		InterimResults: true,
		Punctuate:      true,
		SmartFormat:    true,
		VadEvents:      false,
	}
}

// NewDeepgramSTTService creates a new Deepgram STT service instance.
// Use DefaultConfig() to get a config with sensible defaults and override only what you need.
func NewDeepgramSTTService(config *DeepgramConfig, logger *core.Logger) *DeepgramSTTService {
	if config == nil {
		config = DefaultConfig()
	}
	if config.BaseURL == "" {
		config.BaseURL = "wss://api.deepgram.com"
	}
	if logger == nil {
		logger = core.GetLogger()
	}

	return &DeepgramSTTService{
		config: config,
		logger: logger,
	}
}

// Init initializes the Deepgram STT service
func (d *DeepgramSTTService) Initialize(ctx context.Context) error {
	// Validate configuration
	if d.config.APIKey == "" {
		return fmt.Errorf("Deepgram API key is required")
	}
	d.done = ctx.Done()

	return nil
}

// Cleanup cleans up resources used by the service
func (d *DeepgramSTTService) Cleanup() error {
	// Safely close the WebSocket connection
	d.closeConnection()
	// Set output channels to nil to avoid sending on closed channels
	d.outChan = nil
	d.interimOutputChan = nil
	d.fatalServiceErrorChan = nil
	d.logger.Info("Deepgram STT service cleaned up")
	return nil
}

// Reset resets the service to its initial state
func (d *DeepgramSTTService) Reset() error {
	flushMsg := ListenV1Finalize{Type: "Finalize"}
	if msg, err := json.Marshal(flushMsg); err == nil {
		d.connMu.Lock()
		if d.isConnected && d.conn != nil {
			_ = d.conn.WriteMessage(websocket.TextMessage, msg)
		}
		d.connMu.Unlock()
	}
	return nil
}

func (d *DeepgramSTTService) Flush() error {
	flushMsg := ListenV1Finalize{Type: "Finalize"}
	if msg, err := json.Marshal(flushMsg); err == nil {
		d.connMu.Lock()
		if d.isConnected && d.conn != nil {
			_ = d.conn.WriteMessage(websocket.TextMessage, msg)
		}
		d.connMu.Unlock()
		return nil
	} else {
		return fmt.Errorf("failed to marshal flush message: %w", err)
	}
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
func (d *DeepgramSTTService) SendTranscriptionAudio(chunk core.AudioChunk) error {

	if !d.isConnected || d.conn == nil {
		return fmt.Errorf("not connected to Deepgram")
	}
	converted, convertErr := audio.ConvertAudioChunk(chunk, core.PCM, 1, 16000)
	if convertErr != nil {
		return fmt.Errorf("failed to convert audio chunk: %w", convertErr)
	}
	// Send audio data as binary message
	d.connMu.Lock()
	err := d.conn.WriteMessage(websocket.BinaryMessage, *converted.Data)
	d.connMu.Unlock()
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
				// Only send error if channel is not nil and context is not done
				select {
				case <-d.done:
					return
				default:
					if d.fatalServiceErrorChan != nil {
						select {
						case d.fatalServiceErrorChan <- fmt.Errorf("Deepgram session error: %w", err):
						default:
						}
					}
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
		"Authorization": {"Token " + d.config.APIKey},
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
	base, err := url.Parse(d.config.BaseURL + "/v1/listen")
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

	q.Set("encoding", "linear16")
	q.Set("sample_rate", intToString(16000))
	q.Set("channels", intToString(1))

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
	d.logger.Debugf("Received message from Deepgram: %s", string(message))
	var base struct {
		Type string `json:"type"`
	}

	if err := json.Unmarshal(message, &base); err != nil {
		return fmt.Errorf("failed to parse message type: %w", err)
	}

	switch base.Type {
	case "Results":
		var result ListenV1Results
		if err := json.Unmarshal(message, &result); err != nil {
			return fmt.Errorf("failed to parse results: %w", err)
		}
		d.processResults(result)

	case "Metadata":
		var metadata ListenV1Metadata
		if err := json.Unmarshal(message, &metadata); err != nil {
			return fmt.Errorf("failed to parse metadata: %w", err)
		}
		// Handle metadata if needed

	case "UtteranceEnd":
		var utteranceEnd ListenV1UtteranceEnd
		if err := json.Unmarshal(message, &utteranceEnd); err != nil {
			return fmt.Errorf("failed to parse utterance end: %w", err)
		}
		// Handle utterance end if needed

	case "SpeechStarted":
		var speechStarted ListenV1SpeechStarted
		if err := json.Unmarshal(message, &speechStarted); err != nil {
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
	if transcript == "" {
		d.logger.Debug("Received empty transcript, ignoring")
		return
	}

	if result.IsFinal || result.SpeechFinal || result.FromFinalize {
		d.logger.Debugf("STT Final Result: %s", transcript)
		select {
		case <-d.done:
			// Don't send if context is done
			return
		default:
			if d.outChan != nil {
				select {
				case d.outChan <- transcript:
				default:
				}
			}
		}
	} else {
		d.logger.Debugf("STT Interim Result: %s", transcript)
		select {
		case <-d.done:
			// Don't send if context is done
			return
		default:
			if d.interimOutputChan != nil {
				select {
				case d.interimOutputChan <- transcript:
				default:
				}
			}
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
			d.connMu.Lock()
			if d.isConnected && d.conn != nil {
				keepAliveMsg := ListenV1KeepAlive{Type: "KeepAlive"}
				if msg, err := json.Marshal(keepAliveMsg); err == nil {
					_ = d.conn.WriteMessage(websocket.TextMessage, msg)
				}
			}
			d.connMu.Unlock()
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
		if msg, err := json.Marshal(closeMsg); err == nil {
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
	Type         string  `json:"type"`
	ChannelIndex []int   `json:"channel_index"`
	Duration     float64 `json:"duration"`
	Start        float64 `json:"start"`
	IsFinal      bool    `json:"is_final"`
	SpeechFinal  bool    `json:"speech_final"`
	Channel      struct {
		Alternatives []struct {
			Transcript string  `json:"transcript"`
			Confidence float64 `json:"confidence"`
			Words      []struct {
				Word           string  `json:"word"`
				Start          float64 `json:"start"`
				End            float64 `json:"end"`
				Confidence     float64 `json:"confidence"`
				Speaker        int     `json:"speaker,omitempty"`
				PunctuatedWord string  `json:"punctuated_word,omitempty"`
			} `json:"words"`
		} `json:"alternatives"`
	} `json:"channel"`
	Metadata struct {
		RequestID string `json:"request_id"`
		ModelInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			Arch    string `json:"arch"`
		} `json:"model_info"`
		ModelUUID string `json:"model_uuid"`
	} `json:"metadata"`
	FromFinalize bool `json:"from_finalize,omitempty"`
	Entities     []struct {
		Label      string  `json:"label"`
		Value      string  `json:"value"`
		RawValue   string  `json:"raw_value"`
		Confidence float64 `json:"confidence"`
		StartWord  int     `json:"start_word"`
		EndWord    int     `json:"end_word"`
	} `json:"entities,omitempty"`
}

type ListenV1Metadata struct {
	Type           string  `json:"type"`
	TransactionKey string  `json:"transaction_key"`
	RequestID      string  `json:"request_id"`
	Sha256         string  `json:"sha256"`
	Created        string  `json:"created"`
	Duration       float64 `json:"duration"`
	Channels       int     `json:"channels"`
}

type ListenV1UtteranceEnd struct {
	Type        string  `json:"type"`
	Channel     []int   `json:"channel"`
	LastWordEnd float64 `json:"last_word_end"`
}

type ListenV1SpeechStarted struct {
	Type      string  `json:"type"`
	Channel   []int   `json:"channel"`
	Timestamp float64 `json:"timestamp"`
}

type ListenV1KeepAlive struct {
	Type string `json:"type"`
}

type ListenV1CloseStream struct {
	Type string `json:"type"`
}

type ListenV1Finalize struct {
	Type string `json:"type"`
}
