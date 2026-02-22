package cartesia

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"interactkit/core"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	defaultCartesiaURL        = "wss://api.cartesia.ai/tts/websocket"
	defaultCartesiaModelID    = "sonic-2"
	defaultCartesiaVoiceID    = "a0e99841-438c-4a64-b679-ae501e7d6091" // Helpful Woman
	defaultCartesiaAPIVersion = "2024-11-13"
	defaultCartesiaLanguage   = "en"
)

// CartesiaTTSConfig holds configuration for the Cartesia TTS service.
type CartesiaTTSConfig struct {
	APIKey     string `json:"api_key"`
	BaseURL    string `json:"base_url"`
	ModelID    string `json:"model_id"`
	VoiceID    string `json:"voice_id"`
	Language   string `json:"language"`
	APIVersion string `json:"api_version"`
}

// CartesiaTTS implements the TTSService interface for Cartesia's WebSocket streaming API.
//
// One persistent WebSocket connection is held per session. Text chunks are sent via
// BufferText (continue=true, flush=false). Flush sends continue=false + flush=true and
// rotates to a new context_id. Reset sends a cancel message for the current context and
// rotates to a new context_id — no reconnection required.
type CartesiaTTS struct {
	config CartesiaTTSConfig
	logger *core.Logger

	mu               sync.RWMutex
	reconnectMu      sync.RWMutex // write-locked during reconnection; callers hold RLock
	conn             *websocket.Conn
	session          *cartesiaSession
	ctx              context.Context
	cancel           context.CancelFunc
	heartbeatDone    chan struct{}
	heartbeatStarted bool

	contextID     string // active context_id used when sending requests
	isInitialized bool
}

// cartesiaSession tracks the channels for the currently active TTS session.
type cartesiaSession struct {
	outChan   chan<- core.AudioChunk
	errorChan chan<- error
	doneChan  chan<- bool
	mu        sync.Mutex
}

// ── WebSocket protocol messages ───────────────────────────────────────────────

// cartesiaTTSRequest is sent to Cartesia for each text chunk or flush.
type cartesiaTTSRequest struct {
	ModelID          string            `json:"model_id"`
	Transcript       string            `json:"transcript"`
	Voice            cartesiaVoice     `json:"voice"`
	OutputFmt        cartesiaOutputFmt `json:"output_format"`
	ContextID        string            `json:"context_id"`
	Continue         bool              `json:"continue"`
	Flush            bool              `json:"flush,omitempty"`
	Language         string            `json:"language,omitempty"`
	MaxBufferDelayMs int               `json:"max_buffer_delay_ms,omitempty"`
}

type cartesiaVoice struct {
	Mode string `json:"mode"`
	ID   string `json:"id"`
}

type cartesiaOutputFmt struct {
	Container  string `json:"container"`
	Encoding   string `json:"encoding"`
	SampleRate int    `json:"sample_rate"`
}

// cartesiaCancelRequest cancels an in-progress context.
type cartesiaCancelRequest struct {
	ContextID string `json:"context_id"`
	Cancel    bool   `json:"cancel"`
}

// cartesiaResponse is a text (JSON) frame from Cartesia.
// For audio, Cartesia may either send binary frames (raw PCM) or
// JSON "chunk" frames with base64-encoded audio in the Data field.
type cartesiaResponse struct {
	Type       string `json:"type"`
	ContextID  string `json:"context_id"`
	StatusCode int    `json:"status_code"`
	Done       bool   `json:"done"`
	Error      string `json:"error,omitempty"`
	// Data is present in "chunk" messages when audio is delivered as JSON.
	Data string `json:"data,omitempty"`
}

// ── Constructor ───────────────────────────────────────────────────────────────

// NewCartesiaTTS creates a new Cartesia TTS service with sensible defaults.
func NewCartesiaTTS(config CartesiaTTSConfig, logger *core.Logger) *CartesiaTTS {
	if config.BaseURL == "" {
		config.BaseURL = defaultCartesiaURL
	}
	if config.ModelID == "" {
		config.ModelID = defaultCartesiaModelID
	}
	if config.VoiceID == "" {
		config.VoiceID = defaultCartesiaVoiceID
	}
	if config.APIVersion == "" {
		config.APIVersion = defaultCartesiaAPIVersion
	}
	if config.Language == "" {
		config.Language = defaultCartesiaLanguage
	}
	if logger == nil {
		logger = core.GetLogger()
	}
	return &CartesiaTTS{
		config:        config,
		logger:        logger,
		heartbeatDone: make(chan struct{}),
	}
}

// ── IService ──────────────────────────────────────────────────────────────────

// Initialize validates config and sets up the internal context.
func (c *CartesiaTTS) Initialize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isInitialized {
		return nil
	}
	if c.config.APIKey == "" {
		return errors.New("cartesia: API key is required")
	}

	c.ctx, c.cancel = context.WithCancel(ctx)
	c.heartbeatDone = make(chan struct{})
	c.contextID = newContextID()
	c.isInitialized = true
	return nil
}

// Cleanup shuts down the service, closing any open connection.
func (c *CartesiaTTS) Cleanup() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isInitialized {
		return nil
	}
	if c.cancel != nil {
		c.cancel()
	}
	if c.heartbeatStarted && c.heartbeatDone != nil {
		select {
		case <-c.heartbeatDone:
		case <-time.After(5 * time.Second):
		}
	}
	c.closeConnectionLocked()
	c.session = nil
	c.isInitialized = false
	c.heartbeatStarted = false
	c.logger.Info("Cartesia TTS service cleaned up")
	return nil
}

// Reset cancels the current context and rotates to a fresh context_id.
// Uses Cartesia's native cancel message — no reconnection needed.
func (c *CartesiaTTS) Reset() error {
	c.reconnectMu.RLock()
	defer c.reconnectMu.RUnlock()

	c.mu.Lock()
	if !c.isInitialized {
		c.mu.Unlock()
		return errors.New("cartesia: service not initialized")
	}
	if c.conn == nil || c.session == nil {
		c.mu.Unlock()
		return errors.New("cartesia: no active TTS session")
	}
	oldContextID := c.contextID
	c.contextID = newContextID()
	conn := c.conn
	c.mu.Unlock()

	// Cancel the in-flight generation for the old context.
	cancelMsg := cartesiaCancelRequest{ContextID: oldContextID, Cancel: true}
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := c.sendJSON(conn, cancelMsg); err != nil {
		c.logger.Infof("Cartesia TTS: warning – failed to send cancel for context %s: %v", oldContextID, err)
		// Non-fatal: old audio may still trickle in but will be ignored (different context_id).
	}
	c.logger.Infof("Cartesia TTS: reset – cancelled context %s, new context %s", oldContextID, c.contextID)
	return nil
}

// ── TTSService ────────────────────────────────────────────────────────────────

// StartTTSSession opens a WebSocket connection and prepares for synthesis.
func (c *CartesiaTTS) StartTTSSession(
	outChan chan<- core.AudioChunk,
	errorChan chan<- error,
	doneChan chan<- bool,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isInitialized {
		return errors.New("cartesia: service not initialized")
	}
	if outChan == nil {
		return errors.New("cartesia: outChan cannot be nil")
	}
	if errorChan == nil {
		return errors.New("cartesia: errorChan cannot be nil")
	}
	if doneChan == nil {
		return errors.New("cartesia: doneChan cannot be nil")
	}

	if c.session != nil {
		c.cleanupSessionLocked(c.session)
	}

	conn, err := c.establishConnection()
	if err != nil {
		return fmt.Errorf("cartesia: failed to establish WebSocket connection: %w", err)
	}
	c.conn = conn

	session := &cartesiaSession{
		outChan:   outChan,
		errorChan: errorChan,
		doneChan:  doneChan,
	}
	c.session = session

	go c.handleIncomingMessages(session)
	c.heartbeatStarted = true
	go c.heartbeat()

	return nil
}

// BufferText sends a text chunk to Cartesia with continue=true.
// MaxBufferDelayMs is set to 0 so Cartesia starts generating immediately.
func (c *CartesiaTTS) BufferText(text string) error {
	c.reconnectMu.RLock()
	defer c.reconnectMu.RUnlock()

	c.mu.RLock()
	if !c.isInitialized {
		c.mu.RUnlock()
		return errors.New("cartesia: service not initialized")
	}
	if c.conn == nil || c.session == nil {
		c.mu.RUnlock()
		return errors.New("cartesia: no active TTS session")
	}
	if text == "" {
		c.mu.RUnlock()
		return errors.New("cartesia: text cannot be empty")
	}
	conn := c.conn
	contextID := c.contextID
	c.mu.RUnlock()

	req := c.buildRequest(text, contextID, true, false)
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.sendJSON(conn, req)
}

// Flush finalises the current context (continue=false, flush=true), signalling Cartesia
// to generate audio for all queued text immediately, then rotates to a new context_id.
func (c *CartesiaTTS) Flush() error {
	c.reconnectMu.RLock()
	defer c.reconnectMu.RUnlock()

	c.mu.RLock()
	if !c.isInitialized {
		c.mu.RUnlock()
		return errors.New("cartesia: service not initialized")
	}
	if c.conn == nil || c.session == nil {
		c.mu.RUnlock()
		return errors.New("cartesia: no active TTS session")
	}
	conn := c.conn
	contextID := c.contextID
	c.mu.RUnlock()

	req := c.buildRequest(" ", contextID, false, true)
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := c.sendJSON(conn, req); err != nil {
		return err
	}

	// Rotate context_id so subsequent BufferText calls start a fresh utterance.
	c.mu.Lock()
	c.contextID = newContextID()
	c.mu.Unlock()
	return nil
}

// ── Internal helpers ──────────────────────────────────────────────────────────

func (c *CartesiaTTS) buildRequest(transcript, contextID string, continue_, flush bool) cartesiaTTSRequest {
	return cartesiaTTSRequest{
		ModelID:          c.config.ModelID,
		Transcript:       transcript,
		Voice:            cartesiaVoice{Mode: "id", ID: c.config.VoiceID},
		OutputFmt:        cartesiaOutputFmt{Container: "raw", Encoding: "pcm_s16le", SampleRate: 24000},
		ContextID:        contextID,
		Continue:         continue_,
		Flush:            flush,
		Language:         c.config.Language,
		MaxBufferDelayMs: 0, // start generating immediately, no buffering delay
	}
}

// cartesiaEncodingString maps a core encoding to Cartesia's encoding string.
func cartesiaEncodingString(enc core.AudioEncodingFormat) string {
	switch enc {
	case core.ULAW:
		return "pcm_mulaw"
	default:
		return "pcm_s16le"
	}
}

// newContextID generates a random UUID to use as a Cartesia context_id.
func newContextID() string {
	return uuid.New().String()
}

// ── WebSocket connection management ──────────────────────────────────────────

func (c *CartesiaTTS) establishConnection() (*websocket.Conn, error) {
	const maxRetries = 3
	const baseDelay = 500 * time.Millisecond

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			delay := baseDelay * time.Duration(attempt)
			c.logger.Infof("Cartesia TTS: retrying connection (attempt %d/%d) in %v after: %v",
				attempt+1, maxRetries, delay, lastErr)
			select {
			case <-c.ctx.Done():
				return nil, c.ctx.Err()
			case <-time.After(delay):
			}
		}
		conn, err := c.dialConnection()
		if err != nil {
			lastErr = err
			continue
		}
		return conn, nil
	}
	return nil, fmt.Errorf("cartesia: failed to connect after %d attempts: %w", maxRetries, lastErr)
}

func (c *CartesiaTTS) dialConnection() (*websocket.Conn, error) {
	url := fmt.Sprintf("%s?api_key=%s&cartesia_version=%s",
		c.config.BaseURL,
		c.config.APIKey,
		c.config.APIVersion,
	)

	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second

	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		return nil, err
	}
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	return conn, nil
}

func (c *CartesiaTTS) reconnect() error {
	c.reconnectMu.Lock()
	defer c.reconnectMu.Unlock()

	c.mu.Lock()
	c.closeConnectionLocked()
	c.mu.Unlock()

	conn, err := c.establishConnection()
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	return nil
}

// ── Incoming message loop ─────────────────────────────────────────────────────

func (c *CartesiaTTS) handleIncomingMessages(session *cartesiaSession) {
	defer func() {
		c.mu.Lock()
		if c.session == session {
			c.cleanupSessionLocked(session)
		}
		c.mu.Unlock()
	}()

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		c.mu.RLock()
		conn := c.conn
		c.mu.RUnlock()

		if conn == nil {
			c.logger.Infof("Cartesia TTS: connection nil, attempting reconnect...")
			if err := c.reconnect(); err != nil {
				c.sendError(session, fmt.Errorf("cartesia: reconnect failed: %w", err))
				return
			}
			c.logger.Infof("Cartesia TTS: reconnected successfully")
			continue
		}

		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		msgType, msg, err := conn.ReadMessage()
		if err != nil {
			select {
			case <-c.ctx.Done():
				return
			default:
			}
			c.logger.Infof("Cartesia TTS: read error, attempting reconnect: %v", err)
			if reconErr := c.reconnect(); reconErr != nil {
				c.sendError(session, fmt.Errorf("cartesia: reconnect failed after read error: %w", reconErr))
				return
			}
			c.logger.Infof("Cartesia TTS: reconnected successfully after read error")
			continue
		}

		switch msgType {
		case websocket.BinaryMessage:
			// Raw PCM audio bytes.
			c.logger.Infof("Cartesia TTS: received binary audio frame (%d bytes)", len(msg))
			c.forwardAudio(msg, session)

		case websocket.TextMessage:
			c.handleTextMessage(msg, session)
		}
	}
}

func (c *CartesiaTTS) handleTextMessage(msg []byte, session *cartesiaSession) {
	var resp cartesiaResponse
	if err := json.Unmarshal(msg, &resp); err != nil {
		c.logger.Infof("Cartesia TTS: failed to parse text message: %v – raw: %s", err, string(msg))
		return
	}

	switch resp.Type {
	case "chunk":
		// Some API versions embed base64-encoded audio in the JSON "chunk" message.
		if resp.Data != "" {
			audioData, err := base64.StdEncoding.DecodeString(resp.Data)
			if err != nil {
				c.logger.Infof("Cartesia TTS: failed to decode base64 audio in chunk: %v", err)
				return
			}
			c.logger.Infof("Cartesia TTS: received JSON audio chunk (base64, %d bytes decoded)", len(audioData))
			c.forwardAudio(audioData, session)
		}
	case "error":
		c.sendError(session, fmt.Errorf("cartesia error (status %d): %s", resp.StatusCode, resp.Error))
	case "done":
		c.logger.Infof("Cartesia TTS: context %s done", resp.ContextID)
	// "timestamps", "phoneme_timestamps", etc. are informational and intentionally ignored.
	}
}

// forwardAudio copies the raw audio bytes and sends them to the session output channel.
func (c *CartesiaTTS) forwardAudio(raw []byte, session *cartesiaSession) {
	dataCopy := make([]byte, len(raw))
	copy(dataCopy, raw)
	chunk := core.AudioChunk{
		Data:       &dataCopy,
		SampleRate: 24000,
		Format:     core.PCM,
		Channels:   1,
		Timestamp:  time.Now(),
	}
	select {
	case session.outChan <- chunk:
	default:
		c.logger.Infof("Cartesia TTS: outChan full, dropping audio chunk (%d bytes)", len(raw))
	}
}

// ── Heartbeat ─────────────────────────────────────────────────────────────────

func (c *CartesiaTTS) heartbeat() {
	defer func() {
		c.mu.Lock()
		if c.heartbeatDone != nil {
			close(c.heartbeatDone)
			c.heartbeatDone = nil
		}
		c.mu.Unlock()
	}()

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.mu.RLock()
			conn := c.conn
			c.mu.RUnlock()

			if conn != nil {
				conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					c.logger.Infof("Cartesia TTS: heartbeat ping failed: %v", err)
					c.mu.Lock()
					c.closeConnectionLocked()
					c.mu.Unlock()
				}
			}
		}
	}
}

// ── Utilities ─────────────────────────────────────────────────────────────────

func (c *CartesiaTTS) sendJSON(conn *websocket.Conn, msg interface{}) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("cartesia: failed to marshal message: %w", err)
	}
	c.logger.Infof("Cartesia TTS: sending: %s", string(data))
	return conn.WriteMessage(websocket.TextMessage, data)
}

func (c *CartesiaTTS) sendError(session *cartesiaSession, err error) {
	if session == nil || session.errorChan == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			c.logger.Infof("Cartesia TTS: recovered from panic sending error: %v", r)
		}
	}()
	select {
	case session.errorChan <- err:
	default:
		c.logger.Infof("Cartesia TTS: error channel full or closed: %v", err)
	}
}

func (c *CartesiaTTS) closeConnectionLocked() {
	if c.conn != nil {
		c.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		c.conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		c.conn.Close()
		c.conn = nil
	}
}

func (c *CartesiaTTS) cleanupSessionLocked(session *cartesiaSession) {
	if session == nil {
		return
	}
	session.outChan = nil
	session.errorChan = nil
	session.doneChan = nil
	c.closeConnectionLocked()
	if c.session == session {
		c.session = nil
	}
}

// IsConnected returns whether there is an active WebSocket connection.
func (c *CartesiaTTS) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn != nil && c.session != nil
}
