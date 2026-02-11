package deepgram

import (
	"context"
	"errors"
	"fmt"
	"interactkit/core"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gorilla/websocket"
)

// DepgramTTSConfig holds configuration for the Deepgram TTS service
type DepgramTTSConfig struct {
	APIKey     string
	BaseURL    string
	Model      string
	Encoding   core.AudioEncodingFormat
	SampleRate int
}

// DepgramTTS implements the TTSService interface for Deepgram's TTS WebSocket API
type DepgramTTS struct {
	config DepgramTTSConfig

	mu             sync.RWMutex
	conn           *websocket.Conn
	currentSession *ttsSession
	ctx            context.Context
	cancel         context.CancelFunc

	// For interface compliance
	isInitialized bool
}

// ttsSession represents an active TTS session
type ttsSession struct {
	outChan    chan<- core.AudioChunk
	errorChan  chan<- error
	doneChan   chan<- bool
	sequenceID int
	mu         sync.Mutex
}

// Message types for Deepgram TTS WebSocket protocol
type (
	// Client messages
	speakV1Text struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}

	speakV1Flush struct {
		Type string `json:"type"`
	}

	speakV1Clear struct {
		Type string `json:"type"`
	}

	speakV1Close struct {
		Type string `json:"type"`
	}

	// Server messages
	speakV1Audio struct {
		Data []byte
	}

	speakV1Metadata struct {
		Type         string `json:"type"`
		RequestID    string `json:"request_id"`
		ModelName    string `json:"model_name"`
		ModelVersion string `json:"model_version"`
		ModelUUID    string `json:"model_uuid"`
	}

	speakV1Flushed struct {
		Type       string  `json:"type"`
		SequenceID float64 `json:"sequence_id"`
	}

	speakV1Cleared struct {
		Type       string  `json:"type"`
		SequenceID float64 `json:"sequence_id"`
	}

	speakV1Warning struct {
		Type        string `json:"type"`
		Description string `json:"description"`
		Code        string `json:"code"`
	}
)

// NewDepgramTTS creates a new Deepgram TTS service with the provided config
func NewDepgramTTS(config DepgramTTSConfig) *DepgramTTS {
	// Set defaults
	if config.BaseURL == "" {
		config.BaseURL = "wss://api.deepgram.com/v1/speak"
	}
	if config.Model == "" {
		config.Model = "aura-asteria-en"
	}
	if config.Encoding == 0 { // Default to PCM if not set
		config.Encoding = core.PCM
	}
	if config.SampleRate == 0 {
		config.SampleRate = 24000
	}

	return &DepgramTTS{
		config: config,
	}
}

// encodingToString converts core.AudioEncodingFormat to Deepgram API string
func encodingToString(encoding core.AudioEncodingFormat) string {
	switch encoding {
	case core.PCM:
		return "linear16"
	case core.ULAW:
		return "mulaw"
	case core.ALAW:
		return "alaw"
	default:
		return "linear16"
	}
}

// Init initializes the Deepgram TTS service
func (d *DepgramTTS) Init(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.isInitialized {
		return nil
	}

	if d.config.APIKey == "" {
		return errors.New("Deepgram API key is required")
	}

	d.ctx, d.cancel = context.WithCancel(ctx)
	d.isInitialized = true

	return nil
}

// Cleanup performs cleanup of the Deepgram TTS service
func (d *DepgramTTS) Cleanup() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.isInitialized {
		return nil
	}

	if d.cancel != nil {
		d.cancel()
	}

	if d.conn != nil {
		d.conn.Close()
		d.conn = nil
	}

	d.currentSession = nil
	d.isInitialized = false

	return nil
}

// Reset resets the TTS service state
func (d *DepgramTTS) Reset() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.isInitialized {
		return errors.New("service not initialized")
	}

	// Close existing connection if any
	if d.conn != nil {
		d.conn.Close()
		d.conn = nil
	}

	d.currentSession = nil

	return nil
}

// StartTTSSession starts a new TTS session
func (d *DepgramTTS) StartTTSSession(
	outChan chan<- core.AudioChunk,
	errorChan chan<- error,
	doneChan chan<- bool,
) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.isInitialized {
		return errors.New("service not initialized")
	}

	// Close existing session if any
	if d.currentSession != nil {
		d.closeCurrentSession()
	}

	// Establish WebSocket connection
	conn, err := d.establishConnection()
	if err != nil {
		return fmt.Errorf("failed to establish WebSocket connection: %w", err)
	}

	d.conn = conn

	// Create new session
	session := &ttsSession{
		outChan:   outChan,
		errorChan: errorChan,
		doneChan:  doneChan,
	}

	d.currentSession = session

	// Start message handling goroutines
	go d.handleIncomingMessages(session)
	go d.heartbeat()

	return nil
}

// establishConnection creates a new WebSocket connection to Deepgram
func (d *DepgramTTS) establishConnection() (*websocket.Conn, error) {
	encodingStr := encodingToString(d.config.Encoding)

	url := fmt.Sprintf("%s?model=%s&encoding=%s&sample_rate=%d",
		d.config.BaseURL,
		d.config.Model,
		encodingStr,
		d.config.SampleRate)

	headers := map[string][]string{
		"Authorization": {d.config.APIKey},
	}

	conn, _, err := websocket.DefaultDialer.Dial(url, headers)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

// handleIncomingMessages processes messages received from Deepgram
func (d *DepgramTTS) handleIncomingMessages(session *ttsSession) {
	defer func() {
		d.mu.Lock()
		if d.currentSession == session {
			d.cleanupSession(session)
		}
		d.mu.Unlock()
	}()

	for {
		select {
		case <-d.ctx.Done():
			return
		default:
			messageType, message, err := d.conn.ReadMessage()
			if err != nil {
				select {
				case session.errorChan <- fmt.Errorf("failed to read message: %w", err):
				default:
					// Channel full, log or ignore
				}
				return
			}

			switch messageType {
			case websocket.BinaryMessage:
				// Audio data
				audioChunk := core.AudioChunk{
					Data:       &message,
					SampleRate: d.config.SampleRate,
					Format:     d.config.Encoding,
				}

				select {
				case session.outChan <- audioChunk:
				default:
					// Channel full, log or ignore
				}

			case websocket.TextMessage:
				d.handleTextMessage(message, session)
			}
		}
	}
}

// handleTextMessage processes JSON messages from Deepgram
func (d *DepgramTTS) handleTextMessage(message []byte, session *ttsSession) {
	var base struct {
		Type string `json:"type"`
	}

	if err := sonic.Unmarshal(message, &base); err != nil {
		select {
		case session.errorChan <- fmt.Errorf("failed to parse message: %w", err):
		default:
		}
		return
	}

	switch base.Type {
	case "Metadata":
		var metadata speakV1Metadata
		if err := sonic.Unmarshal(message, &metadata); err == nil {
			// Metadata received, can be logged or ignored
			// Could be extended to emit metadata events
		}

	case "Flushed":
		var flushed speakV1Flushed
		if err := sonic.Unmarshal(message, &flushed); err == nil {
			// Flush complete
			select {
			case session.doneChan <- true:
			default:
			}
		}

	case "Cleared":
		var cleared speakV1Cleared
		if err := sonic.Unmarshal(message, &cleared); err == nil {
			// Clear complete
			select {
			case session.doneChan <- true:
			default:
			}
		}

	case "Warning":
		var warning speakV1Warning
		if err := sonic.Unmarshal(message, &warning); err == nil {
			select {
			case session.errorChan <- fmt.Errorf("Deepgram warning: %s (code: %s)",
				warning.Description, warning.Code):
			default:
			}
		}
	}
}

// BufferText sends text to be converted to speech
func (d *DepgramTTS) BufferText(text string) error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if !d.isInitialized {
		return errors.New("service not initialized")
	}

	if d.conn == nil || d.currentSession == nil {
		return errors.New("no active TTS session")
	}

	msg := speakV1Text{
		Type: "Speak",
		Text: text,
	}

	return d.sendJSON(msg)
}

// Flush flushes the current buffer
func (d *DepgramTTS) Flush() error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if !d.isInitialized {
		return errors.New("service not initialized")
	}

	if d.conn == nil || d.currentSession == nil {
		return errors.New("no active TTS session")
	}

	msg := speakV1Flush{
		Type: "Flush",
	}

	return d.sendJSON(msg)
}

// ResetSession clears the buffer and starts a new audio generation
func (d *DepgramTTS) ResetSession() error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if !d.isInitialized {
		return errors.New("service not initialized")
	}

	if d.conn == nil || d.currentSession == nil {
		return errors.New("no active TTS session")
	}

	msg := speakV1Clear{
		Type: "Clear",
	}

	return d.sendJSON(msg)
}

// CloseSession gracefully closes the current session
func (d *DepgramTTS) CloseSession() error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if !d.isInitialized {
		return errors.New("service not initialized")
	}

	if d.conn == nil || d.currentSession == nil {
		return nil
	}

	msg := speakV1Close{
		Type: "Close",
	}

	return d.sendJSON(msg)
}

// sendJSON sends a JSON message over WebSocket
func (d *DepgramTTS) sendJSON(msg interface{}) error {
	data, err := sonic.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	return d.conn.WriteMessage(websocket.TextMessage, data)
}

// heartbeat sends periodic pings to keep the connection alive
func (d *DepgramTTS) heartbeat() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			d.mu.RLock()
			if d.conn != nil {
				d.conn.WriteMessage(websocket.PingMessage, nil)
			}
			d.mu.RUnlock()
		}
	}
}

// closeCurrentSession closes the current session without cleanup
func (d *DepgramTTS) closeCurrentSession() {
	if d.conn != nil {
		d.conn.Close()
		d.conn = nil
	}
	d.currentSession = nil
}

// cleanupSession performs cleanup for a specific session
func (d *DepgramTTS) cleanupSession(session *ttsSession) {
	// Safely close channels
	d.safeCloseChan(session.outChan)
	d.safeCloseChan(session.errorChan)
	d.safeCloseChan(session.doneChan)

	if d.conn != nil {
		d.conn.Close()
		d.conn = nil
	}

	if d.currentSession == session {
		d.currentSession = nil
	}
}

// safeCloseChan safely closes a channel if it's not nil
func (d *DepgramTTS) safeCloseChan(ch interface{}) {
	defer func() {
		if r := recover(); r != nil {
			// Channel already closed or not closable
		}
	}()

	switch c := ch.(type) {
	case chan core.AudioChunk:
		if c != nil {
			close(c)
		}
	case chan error:
		if c != nil {
			close(c)
		}
	case chan bool:
		if c != nil {
			close(c)
		}
	}
}

// getNextSequenceID returns the next sequence ID for audio chunks
func (s *ttsSession) getNextSequenceID() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequenceID++
	return s.sequenceID
}

// GetConfig returns the current configuration
func (d *DepgramTTS) GetConfig() DepgramTTSConfig {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.config
}

// UpdateConfig updates the configuration (note: may require reconnection)
func (d *DepgramTTS) UpdateConfig(config DepgramTTSConfig) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.isInitialized {
		return errors.New("service not initialized")
	}

	// Validate required fields
	if config.APIKey == "" {
		return errors.New("API key cannot be empty")
	}

	d.config = config
	return nil
}
