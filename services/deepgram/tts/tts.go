package deepgram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"interactkit/core"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// maxCharsBeforeFlush is the character limit before an automatic flush is triggered.
// Deepgram returns DATA-0001 (1008) if too many characters are buffered between flushes.
const maxCharsBeforeFlush = 2000

// DepgramTTSConfig holds configuration for the Deepgram TTS service
type DepgramTTSConfig struct {
	APIKey  string `json:"api_key"`
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
}

// DefaultConfig returns a DepgramTTSConfig with sensible defaults
func DefaultConfig() DepgramTTSConfig {
	return DepgramTTSConfig{
		BaseURL: "wss://api.deepgram.com/v1/speak",
		Model:   "aura-2-arcas-en",
	}
}

// DepgramTTS implements the TTSService interface for Deepgram's TTS WebSocket API
type DepgramTTS struct {
	config DepgramTTSConfig
	logger *core.Logger

	mu               sync.RWMutex
	reconnectMu      sync.RWMutex // write-locked during reconnection; callers block until done
	conn             *websocket.Conn
	currentSession   *ttsSession
	ctx              context.Context
	cancel           context.CancelFunc
	heartbeatDone    chan struct{}
	heartbeatStarted bool

	// charCount tracks buffered characters since the last flush to avoid DATA-0001 (1008).
	charCount atomic.Int64

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

	speakV1KeepAlive struct {
		Type string `json:"type"`
	}

	// Server messages
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

	speakV1Error struct {
		Type        string `json:"type"`
		Description string `json:"description"`
		Code        string `json:"code"`
	}
)

// NewDepgramTTS creates a new Deepgram TTS service with the provided config.
// Use DefaultConfig() to get a config with sensible defaults and override only what you need.
func NewDepgramTTS(config DepgramTTSConfig, logger *core.Logger) *DepgramTTS {
	defaults := DefaultConfig()
	if config.BaseURL == "" {
		config.BaseURL = defaults.BaseURL
	}
	if config.Model == "" {
		config.Model = defaults.Model
	}
	if logger == nil {
		logger = core.GetLogger()
	}
	return &DepgramTTS{
		config:        config,
		logger:        logger,
		heartbeatDone: make(chan struct{}),
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
func (d *DepgramTTS) Initialize(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.isInitialized {
		return nil
	}

	if d.config.APIKey == "" {
		return errors.New("Deepgram API key is required")
	}

	d.ctx, d.cancel = context.WithCancel(ctx)
	d.heartbeatDone = make(chan struct{})
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

	// Wait for heartbeat to finish with timeout, only if it was started
	if d.heartbeatStarted && d.heartbeatDone != nil {
		select {
		case <-d.heartbeatDone:
		case <-time.After(5 * time.Second):
			// Timeout waiting for heartbeat, continue cleanup
		}
	}

	d.closeConnectionLocked()
	d.currentSession = nil
	d.isInitialized = false
	d.heartbeatStarted = false
	d.logger.Info("Deepgram TTS service cleaned up")

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

	// Validate channels - they MUST not be nil
	if outChan == nil {
		return errors.New("outChan cannot be nil")
	}
	if errorChan == nil {
		return errors.New("errorChan cannot be nil")
	}
	if doneChan == nil {
		return errors.New("doneChan cannot be nil")
	}

	// Close existing session if any
	if d.currentSession != nil {
		d.cleanupSessionLocked(d.currentSession)
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
	d.heartbeatStarted = true
	go d.heartbeat()

	return nil
}

// establishConnection creates a new WebSocket connection to Deepgram with retry logic
func (d *DepgramTTS) establishConnection() (*websocket.Conn, error) {
	const maxRetries = 3
	const baseDelay = 500 * time.Millisecond

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			delay := baseDelay * time.Duration(attempt)
			d.logger.Infof("Deepgram TTS: retrying connection (attempt %d/%d) in %v after error: %v",
				attempt+1, maxRetries, delay, lastErr)
			select {
			case <-d.ctx.Done():
				return nil, d.ctx.Err()
			case <-time.After(delay):
			}
		}

		conn, err := d.dialConnection()
		if err != nil {
			lastErr = err
			continue
		}
		return conn, nil
	}

	return nil, fmt.Errorf("failed to connect after %d attempts: %w", maxRetries, lastErr)
}

// dialConnection performs a single WebSocket dial attempt to Deepgram
func (d *DepgramTTS) dialConnection() (*websocket.Conn, error) {
	url := fmt.Sprintf("%s?model=%s&encoding=linear16&sample_rate=24000",
		d.config.BaseURL,
		d.config.Model)

	// IMPORTANT FIX: Deepgram requires "Token " prefix for API key
	headers := map[string][]string{
		"Authorization": {fmt.Sprintf("Token %s", d.config.APIKey)},
	}

	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second

	conn, _, err := dialer.Dial(url, headers)
	if err != nil {
		return nil, err
	}

	// Set read/write deadlines
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	return conn, nil
}

// handleIncomingMessages processes messages received from Deepgram, reconnecting on errors
func (d *DepgramTTS) handleIncomingMessages(session *ttsSession) {
	defer func() {
		d.mu.Lock()
		if d.currentSession == session {
			d.cleanupSessionLocked(session)
		}
		d.mu.Unlock()
	}()

	for {
		// Check context first
		select {
		case <-d.ctx.Done():
			return
		default:
		}

		d.mu.RLock()
		conn := d.conn
		d.mu.RUnlock()

		if conn == nil {
			// Connection was closed (e.g. by heartbeat ping failure), attempt reconnect
			d.logger.Infof("Deepgram TTS: connection lost, attempting reconnect...")
			if err := d.reconnect(); err != nil {
				d.sendError(session, fmt.Errorf("reconnect failed: %w", err))
				return
			}
			d.logger.Infof("Deepgram TTS: reconnected successfully")
			continue
		}

		// Reset read deadline
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))

		messageType, message, err := conn.ReadMessage()
		if err != nil {
			select {
			case <-d.ctx.Done():
				return
			default:
			}

			d.logger.Infof("Deepgram TTS: read error, attempting reconnect: %v", err)
			if reconnErr := d.reconnect(); reconnErr != nil {
				d.sendError(session, fmt.Errorf("reconnect failed after read error: %w", reconnErr))
				return
			}
			d.logger.Infof("Deepgram TTS: reconnected successfully after read error")
			continue
		}

		switch messageType {
		case websocket.BinaryMessage:
			// Audio data - need to make a copy since message buffer will be reused
			audioData := make([]byte, len(message))
			copy(audioData, message)

			audioChunk := core.AudioChunk{
				Data:       &audioData,
				SampleRate: 24000,
				Format:     core.PCM,
				Channels:   1,
			}

			select {
			case session.outChan <- audioChunk:
			default:
				// Channel full, log or ignore
				d.logger.Infof("Warning: outChan full, dropping audio chunk")
			}

		case websocket.TextMessage:
			d.handleTextMessage(message, session)
		}
	}
}

// reconnect closes the current connection and establishes a new one.
// It holds reconnectMu write-locked for the entire duration, so callers
// blocked on reconnectMu.RLock() (BufferText, Flush, Reset) will wait
// instead of returning "no active TTS session".
func (d *DepgramTTS) reconnect() error {
	d.reconnectMu.Lock()
	defer d.reconnectMu.Unlock()

	d.mu.Lock()
	d.closeConnectionLocked()
	d.mu.Unlock()

	conn, err := d.establishConnection()
	if err != nil {
		return err
	}

	d.mu.Lock()
	d.conn = conn
	d.mu.Unlock()

	// New connection has an empty buffer; reset the counter so auto-flush
	// thresholds are accurate from the start.
	d.charCount.Store(0)
	return nil
}

// handleTextMessage processes JSON messages from Deepgram
func (d *DepgramTTS) handleTextMessage(message []byte, session *ttsSession) {
	var base struct {
		Type string `json:"type"`
	}

	if err := json.Unmarshal(message, &base); err != nil {
		d.sendError(session, fmt.Errorf("failed to parse message: %w", err))
		return
	}

	switch base.Type {
	case "Metadata":
		var metadata speakV1Metadata
		if err := json.Unmarshal(message, &metadata); err != nil {
			d.sendError(session, fmt.Errorf("failed to parse metadata: %w", err))
		}
		// Metadata received, can be logged or ignored
		d.logger.Infof("TTS Metadata received: model=%s", metadata.ModelName)

	case "Flushed":
		var flushed speakV1Flushed
		if err := json.Unmarshal(message, &flushed); err == nil {
			// Flush complete - just log, don't signal done as more audio may come
			d.logger.Infof("TTS Flush complete, sequence_id: %v", flushed.SequenceID)
		}

	case "Cleared":
		var cleared speakV1Cleared
		if err := json.Unmarshal(message, &cleared); err == nil {
			// Clear complete - just log, don't signal done as connection is still active
			d.logger.Infof("TTS Clear complete, sequence_id: %v", cleared.SequenceID)
		}

	case "Warning":
		var warning speakV1Warning
		if err := json.Unmarshal(message, &warning); err == nil {
			// Log warnings but don't treat as fatal errors
			d.logger.Infof("Deepgram TTS warning: %s (code: %s)", warning.Description, warning.Code)
		}

	case "Error":
		var errMsg speakV1Error
		if err := json.Unmarshal(message, &errMsg); err == nil {
			d.sendError(session, fmt.Errorf("Deepgram error: %s (code: %s)",
				errMsg.Description, errMsg.Code))
		}
	}
}

// sendError safely sends an error to the session's error channel
func (d *DepgramTTS) sendError(session *ttsSession, err error) {
	if session == nil || session.errorChan == nil {
		return
	}

	// Don't hold mutex while sending on channel
	defer func() {
		if r := recover(); r != nil {
			d.logger.Infof("Recovered from panic sending error on channel: %v", r)
		}
	}()
	select {
	case session.errorChan <- err:
	default:
		// Channel full or closed, log if needed
		d.logger.Infof("Error channel full or closed: %v", err)
	}
}

// BufferText sends text to be converted to speech.
// Text larger than maxCharsBeforeFlush is automatically split into safe-sized
// chunks, each followed by a Flush, to avoid DATA-0001 from Deepgram.
func (d *DepgramTTS) BufferText(text string) error {
	// Block while a reconnect is in progress
	d.reconnectMu.RLock()
	defer d.reconnectMu.RUnlock()

	d.mu.RLock()
	if !d.isInitialized {
		d.mu.RUnlock()
		return errors.New("service not initialized")
	}

	if d.conn == nil || d.currentSession == nil {
		d.mu.RUnlock()
		return errors.New("no active TTS session")
	}

	if text == "" {
		d.mu.RUnlock()
		return errors.New("text cannot be empty")
	}

	conn := d.conn
	d.mu.RUnlock()

	// If the text itself exceeds the limit, send it in chunks.  Each chunk is
	// flushed immediately so Deepgram's buffer never fills up.
	const chunkSize = maxCharsBeforeFlush - 100 // leave headroom
	for len(text) > chunkSize {
		chunk := text[:chunkSize]
		text = text[chunkSize:]

		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := d.sendJSON(conn, speakV1Text{Type: "Speak", Text: chunk}); err != nil {
			return err
		}
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := d.sendJSON(conn, speakV1Flush{Type: "Flush"}); err != nil {
			return err
		}
		d.charCount.Store(0)
	}

	// Auto-flush if adding this (remaining) text would exceed the limit.
	if d.charCount.Add(int64(len(text))) >= maxCharsBeforeFlush {
		d.charCount.Store(int64(len(text)))
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := d.sendJSON(conn, speakV1Flush{Type: "Flush"}); err != nil {
			d.logger.Infof("Deepgram TTS: auto-flush failed: %v", err)
		} else {
			d.logger.Infof("Deepgram TTS: auto-flushed at character limit (%d)", maxCharsBeforeFlush)
		}
	}

	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return d.sendJSON(conn, speakV1Text{Type: "Speak", Text: text})
}

// Flush flushes the current buffer
func (d *DepgramTTS) Flush() error {
	// Block while a reconnect is in progress
	d.reconnectMu.RLock()
	defer d.reconnectMu.RUnlock()

	d.mu.RLock()
	if !d.isInitialized {
		d.mu.RUnlock()
		return errors.New("service not initialized")
	}

	if d.conn == nil || d.currentSession == nil {
		d.mu.RUnlock()
		return errors.New("no active TTS session")
	}

	conn := d.conn
	d.mu.RUnlock()

	d.charCount.Store(0)

	msg := speakV1Flush{
		Type: "Flush",
	}

	return d.sendJSON(conn, msg)
}

// ResetSession clears the buffer and starts a new audio generation
func (d *DepgramTTS) Reset() error {
	// Block while a reconnect is in progress
	d.reconnectMu.RLock()
	defer d.reconnectMu.RUnlock()

	d.mu.RLock()
	if !d.isInitialized {
		d.mu.RUnlock()
		return errors.New("service not initialized")
	}

	if d.conn == nil || d.currentSession == nil {
		d.mu.RUnlock()
		return errors.New("no active TTS session")
	}

	conn := d.conn
	d.mu.RUnlock()

	d.charCount.Store(0)

	msg := speakV1Clear{
		Type: "Clear",
	}

	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return d.sendJSON(conn, msg)
}

// CloseSession gracefully closes the current session
func (d *DepgramTTS) CloseSession() error {
	// Block while a reconnect is in progress
	d.reconnectMu.RLock()
	defer d.reconnectMu.RUnlock()

	d.mu.RLock()
	if !d.isInitialized {
		d.mu.RUnlock()
		return errors.New("service not initialized")
	}

	if d.conn == nil || d.currentSession == nil {
		d.mu.RUnlock()
		return nil
	}

	conn := d.conn
	d.mu.RUnlock()

	msg := speakV1Close{
		Type: "Close",
	}

	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return d.sendJSON(conn, msg)
}

// sendJSON sends a JSON message over WebSocket
func (d *DepgramTTS) sendJSON(conn *websocket.Conn, msg interface{}) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	d.logger.Infof("Sending message to Deepgram: %s\n", string(data))

	return conn.WriteMessage(websocket.TextMessage, data)
}

// heartbeat sends periodic pings to keep the connection alive
func (d *DepgramTTS) heartbeat() {
	defer func() {
		d.mu.Lock()
		if d.heartbeatDone != nil {
			close(d.heartbeatDone)
			d.heartbeatDone = nil
		}
		d.mu.Unlock()
	}()

	// 8 seconds is safely under Deepgram's ~10 s idle-connection timeout.
	ticker := time.NewTicker(8 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			d.mu.RLock()
			conn := d.conn
			d.mu.RUnlock()

			if conn != nil {
				conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				// The speak API does not support application-level KeepAlive
				// messages; use a WebSocket PING instead (required by RFC 6455).
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					// Close the dead connection; handleIncomingMessages will reconnect.
					d.logger.Infof("Deepgram TTS: heartbeat ping failed, closing connection: %v", err)
					d.mu.Lock()
					d.closeConnectionLocked()
					d.mu.Unlock()
				}
			}
		}
	}
}

// closeConnectionLocked safely closes the WebSocket connection (must be called with lock held)
func (d *DepgramTTS) closeConnectionLocked() {
	if d.conn != nil {
		// Send close message gracefully
		d.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		d.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		d.conn.Close()
		d.conn = nil
	}
}

// cleanupSessionLocked performs cleanup for a specific session (must be called with lock held)
func (d *DepgramTTS) cleanupSessionLocked(session *ttsSession) {
	if session == nil {
		return
	}

	// Don't close channels that were passed in - ownership belongs to caller
	// Just nil them out in our reference
	session.outChan = nil
	session.errorChan = nil
	session.doneChan = nil

	d.closeConnectionLocked()

	if d.currentSession == session {
		d.currentSession = nil
	}
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

	// Check if critical config changed that requires reconnection
	requiresReconnect := d.config.APIKey != config.APIKey ||
		d.config.BaseURL != config.BaseURL ||
		d.config.Model != config.Model

	d.config = config

	// If critical config changed and we have an active session, close it
	if requiresReconnect && d.currentSession != nil {
		d.cleanupSessionLocked(d.currentSession)
	}

	return nil
}

// IsConnected returns whether the service has an active WebSocket connection
func (d *DepgramTTS) IsConnected() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.conn != nil && d.currentSession != nil
}
