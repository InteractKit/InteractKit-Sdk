package elevenlabs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"interactkit/core"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ElevenLabsTTSConfig holds configuration for the ElevenLabs TTS service
type ElevenLabsTTSConfig struct {
	APIKey  string `json:"api_key"`
	BaseURL string `json:"base_url"`
	VoiceID string `json:"voice_id"`
	ModelID string `json:"model_id"`

	// Voice settings
	Stability       float64 `json:"stability"`
	SimilarityBoost float64 `json:"similarity_boost"`
}

// ElevenLabsTTS implements the TTS service interface for ElevenLabs WebSocket streaming API
type ElevenLabsTTS struct {
	config ElevenLabsTTSConfig
	logger *core.Logger

	mu               sync.RWMutex
	reconnectMu      sync.RWMutex // write-locked during reconnection; callers block until done
	conn             *websocket.Conn
	currentSession   *ttsSession
	ctx              context.Context
	cancel           context.CancelFunc
	heartbeatDone    chan struct{}
	heartbeatStarted bool

	isInitialized bool
}

// ttsSession represents an active TTS session
type ttsSession struct {
	outChan   chan<- core.AudioChunk
	errorChan chan<- error
	doneChan  chan<- bool
	mu        sync.Mutex
}

// Client messages
type (
	// BOS (Beginning of Stream) - sent once on connect
	elBOSMessage struct {
		Text             string          `json:"text"`
		VoiceSettings    elVoiceSettings `json:"voice_settings"`
		GenerationConfig elGenConfig     `json:"generation_config"`
	}

	elVoiceSettings struct {
		Stability       float64 `json:"stability"`
		SimilarityBoost float64 `json:"similarity_boost"`
	}

	elGenConfig struct {
		ChunkLengthSchedule []int `json:"chunk_length_schedule"`
	}

	// Text chunk message
	elTextMessage struct {
		Text string `json:"text"`
	}
)

// Server messages
type (
	// Audio response from ElevenLabs (base64-encoded audio)
	elAudioMessage struct {
		Audio              string              `json:"audio"`
		IsFinal            bool                `json:"isFinal"`
		NormalizedAlignment *elAlignmentData   `json:"normalizedAlignment,omitempty"`
	}

	elAlignmentData struct {
		CharStartTimesMs []int    `json:"charStartTimesMs"`
		CharDurationsMs  []int    `json:"charDurationsMs"`
		Chars            []string `json:"chars"`
	}

	// Error response
	elErrorMessage struct {
		Error   string `json:"error"`
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
)

// NewElevenLabsTTS creates a new ElevenLabs TTS service with the provided config
func NewElevenLabsTTS(config ElevenLabsTTSConfig, logger *core.Logger) *ElevenLabsTTS {
	if config.BaseURL == "" {
		config.BaseURL = "wss://api.elevenlabs.io/v1/text-to-speech"
	}
	if config.VoiceID == "" {
		config.VoiceID = "21m00Tcm4TlvDq8ikWAM" // Default: Rachel
	}
	if config.ModelID == "" {
		config.ModelID = "eleven_turbo_v2_5"
	}
	if config.Stability == 0 {
		config.Stability = 0.5
	}
	if config.SimilarityBoost == 0 {
		config.SimilarityBoost = 0.75
	}

	if logger == nil {
		logger = core.GetLogger()
	}
	return &ElevenLabsTTS{
		config:        config,
		logger:        logger,
		heartbeatDone: make(chan struct{}),
	}
}

// outputFormatString converts config encoding + sample rate to ElevenLabs output_format param
func outputFormatString(encoding core.AudioEncodingFormat, sampleRate int) string {
	switch encoding {
	case core.ULAW:
		return "ulaw_8000"
	case core.PCM:
		switch sampleRate {
		case 16000:
			return "pcm_16000"
		case 22050:
			return "pcm_22050"
		case 44100:
			return "pcm_44100"
		default:
			return "pcm_24000"
		}
	default:
		return "pcm_24000"
	}
}

// Initialize initializes the ElevenLabs TTS service
func (e *ElevenLabsTTS) Initialize(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.isInitialized {
		return nil
	}

	if e.config.APIKey == "" {
		return errors.New("ElevenLabs API key is required")
	}

	e.ctx, e.cancel = context.WithCancel(ctx)
	e.heartbeatDone = make(chan struct{})
	e.isInitialized = true

	return nil
}

// Cleanup performs cleanup of the ElevenLabs TTS service
func (e *ElevenLabsTTS) Cleanup() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.isInitialized {
		return nil
	}

	if e.cancel != nil {
		e.cancel()
	}

	if e.heartbeatStarted && e.heartbeatDone != nil {
		select {
		case <-e.heartbeatDone:
		case <-time.After(5 * time.Second):
		}
	}

	e.closeConnectionLocked()
	e.currentSession = nil
	e.isInitialized = false
	e.heartbeatStarted = false
	e.logger.Info("ElevenLabs TTS service cleaned up")

	return nil
}

// StartTTSSession starts a new TTS session
func (e *ElevenLabsTTS) StartTTSSession(
	outChan chan<- core.AudioChunk,
	errorChan chan<- error,
	doneChan chan<- bool,
) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.isInitialized {
		return errors.New("service not initialized")
	}
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
	if e.currentSession != nil {
		e.cleanupSessionLocked(e.currentSession)
	}

	conn, err := e.establishConnection()
	if err != nil {
		return fmt.Errorf("failed to establish WebSocket connection: %w", err)
	}

	e.conn = conn

	session := &ttsSession{
		outChan:   outChan,
		errorChan: errorChan,
		doneChan:  doneChan,
	}
	e.currentSession = session

	// Send BOS (Beginning of Stream) to start the session
	if err := e.sendBOSLocked(conn); err != nil {
		e.closeConnectionLocked()
		e.currentSession = nil
		return fmt.Errorf("failed to send BOS: %w", err)
	}

	go e.handleIncomingMessages(session)
	e.heartbeatStarted = true
	go e.heartbeat()

	return nil
}

// establishConnection creates a new WebSocket connection with retry logic
func (e *ElevenLabsTTS) establishConnection() (*websocket.Conn, error) {
	const maxRetries = 3
	const baseDelay = 500 * time.Millisecond

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			delay := baseDelay * time.Duration(attempt)
			e.logger.Infof("ElevenLabs TTS: retrying connection (attempt %d/%d) in %v after error: %v",
				attempt+1, maxRetries, delay, lastErr)
			select {
			case <-e.ctx.Done():
				return nil, e.ctx.Err()
			case <-time.After(delay):
			}
		}

		conn, err := e.dialConnection()
		if err != nil {
			lastErr = err
			continue
		}
		return conn, nil
	}

	return nil, fmt.Errorf("failed to connect after %d attempts: %w", maxRetries, lastErr)
}

// dialConnection performs a single WebSocket dial to ElevenLabs
func (e *ElevenLabsTTS) dialConnection() (*websocket.Conn, error) {
	outputFormat := outputFormatString(core.PCM, 24000)

	url := fmt.Sprintf("%s/%s/stream-input?model_id=%s&output_format=%s",
		e.config.BaseURL,
		e.config.VoiceID,
		e.config.ModelID,
		outputFormat,
	)

	headers := map[string][]string{
		"xi-api-key": {e.config.APIKey},
	}

	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second

	conn, _, err := dialer.Dial(url, headers)
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

// sendBOSLocked sends the Beginning of Stream message (must hold lock or be called safely)
func (e *ElevenLabsTTS) sendBOSLocked(conn *websocket.Conn) error {
	bos := elBOSMessage{
		Text: " ",
		VoiceSettings: elVoiceSettings{
			Stability:       e.config.Stability,
			SimilarityBoost: e.config.SimilarityBoost,
		},
		GenerationConfig: elGenConfig{
			ChunkLengthSchedule: []int{120, 160, 250, 290},
		},
	}
	return e.sendJSON(conn, bos)
}

// handleIncomingMessages processes messages received from ElevenLabs
func (e *ElevenLabsTTS) handleIncomingMessages(session *ttsSession) {
	defer func() {
		e.mu.Lock()
		if e.currentSession == session {
			e.cleanupSessionLocked(session)
		}
		e.mu.Unlock()
	}()

	for {
		select {
		case <-e.ctx.Done():
			return
		default:
		}

		e.mu.RLock()
		conn := e.conn
		e.mu.RUnlock()

		if conn == nil {
			e.logger.Infof("ElevenLabs TTS: connection lost, attempting reconnect...")
			if err := e.reconnect(); err != nil {
				e.sendError(session, fmt.Errorf("reconnect failed: %w", err))
				return
			}
			e.logger.Infof("ElevenLabs TTS: reconnected successfully")
			continue
		}

		conn.SetReadDeadline(time.Now().Add(60 * time.Second))

		messageType, message, err := conn.ReadMessage()
		if err != nil {
			select {
			case <-e.ctx.Done():
				return
			default:
			}

			e.logger.Infof("ElevenLabs TTS: read error, attempting reconnect: %v", err)
			if reconnErr := e.reconnect(); reconnErr != nil {
				e.sendError(session, fmt.Errorf("reconnect failed after read error: %w", reconnErr))
				return
			}
			e.logger.Infof("ElevenLabs TTS: reconnected successfully after read error")
			continue
		}

		switch messageType {
		case websocket.TextMessage:
			e.handleTextMessage(message, session)
		case websocket.BinaryMessage:
			// ElevenLabs sends audio as base64 in JSON text messages, but handle
			// binary frames defensively just in case
			audioData := make([]byte, len(message))
			copy(audioData, message)
			chunk := core.AudioChunk{
				Data:       &audioData,
				SampleRate: 24000,
				Format:     core.PCM,
				Channels:   1,
				Timestamp:  time.Now(),
			}
			select {
			case session.outChan <- chunk:
			default:
				e.logger.Infof("ElevenLabs TTS: outChan full, dropping binary audio chunk")
			}
		}
	}
}

// handleTextMessage processes JSON messages from ElevenLabs
func (e *ElevenLabsTTS) handleTextMessage(message []byte, session *ttsSession) {
	// ElevenLabs sends either audio JSON or error JSON
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(message, &raw); err != nil {
		e.logger.Infof("ElevenLabs TTS: failed to parse message: %v", err)
		return
	}

	// Check for error
	if errField, ok := raw["error"]; ok && string(errField) != "null" {
		var errMsg elErrorMessage
		if err := json.Unmarshal(message, &errMsg); err == nil {
			e.sendError(session, fmt.Errorf("ElevenLabs error: %s (code: %d)", errMsg.Message, errMsg.Code))
		} else {
			e.sendError(session, fmt.Errorf("ElevenLabs error: %s", string(errField)))
		}
		return
	}

	// Check for audio
	if audioField, ok := raw["audio"]; ok && string(audioField) != "null" {
		var audioMsg elAudioMessage
		if err := json.Unmarshal(message, &audioMsg); err != nil {
			e.logger.Infof("ElevenLabs TTS: failed to parse audio message: %v", err)
			return
		}

		if audioMsg.Audio != "" {
			audioData, err := base64.StdEncoding.DecodeString(audioMsg.Audio)
			if err != nil {
				e.logger.Infof("ElevenLabs TTS: failed to decode audio: %v", err)
				return
			}

			dataCopy := make([]byte, len(audioData))
			copy(dataCopy, audioData)

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
				e.logger.Infof("ElevenLabs TTS: outChan full, dropping audio chunk")
			}
		}

		if audioMsg.IsFinal {
			e.logger.Infof("ElevenLabs TTS: received isFinal=true, generation complete")
		}
	}
}

// reconnect closes the current connection and establishes a new one, then sends BOS
func (e *ElevenLabsTTS) reconnect() error {
	e.reconnectMu.Lock()
	defer e.reconnectMu.Unlock()

	e.mu.Lock()
	e.closeConnectionLocked()
	e.mu.Unlock()

	conn, err := e.establishConnection()
	if err != nil {
		return err
	}

	if err := e.sendBOSLocked(conn); err != nil {
		conn.Close()
		return fmt.Errorf("failed to send BOS after reconnect: %w", err)
	}

	e.mu.Lock()
	e.conn = conn
	e.mu.Unlock()

	return nil
}

// sendError safely sends an error to the session's error channel
func (e *ElevenLabsTTS) sendError(session *ttsSession, err error) {
	if session == nil || session.errorChan == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			e.logger.Infof("ElevenLabs TTS: recovered from panic sending error: %v", r)
		}
	}()
	select {
	case session.errorChan <- err:
	default:
		e.logger.Infof("ElevenLabs TTS: error channel full or closed: %v", err)
	}
}

// BufferText sends a text chunk to ElevenLabs for synthesis
func (e *ElevenLabsTTS) BufferText(text string) error {
	e.reconnectMu.RLock()
	defer e.reconnectMu.RUnlock()

	e.mu.RLock()
	if !e.isInitialized {
		e.mu.RUnlock()
		return errors.New("service not initialized")
	}
	if e.conn == nil || e.currentSession == nil {
		e.mu.RUnlock()
		return errors.New("no active TTS session")
	}
	if text == "" {
		e.mu.RUnlock()
		return errors.New("text cannot be empty")
	}
	conn := e.conn
	e.mu.RUnlock()

	msg := elTextMessage{Text: text}
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return e.sendJSON(conn, msg)
}

// Flush signals end of stream (EOS) to ElevenLabs, causing it to generate remaining audio
// then close the connection. The service will reconnect automatically for the next session.
func (e *ElevenLabsTTS) Flush() error {
	e.reconnectMu.RLock()
	defer e.reconnectMu.RUnlock()

	e.mu.RLock()
	if !e.isInitialized {
		e.mu.RUnlock()
		return errors.New("service not initialized")
	}
	if e.conn == nil || e.currentSession == nil {
		e.mu.RUnlock()
		return errors.New("no active TTS session")
	}
	conn := e.conn
	e.mu.RUnlock()

	// EOS: empty text signals ElevenLabs to finish generation
	msg := elTextMessage{Text: ""}
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return e.sendJSON(conn, msg)
}

// Reset clears the current generation by closing and reconnecting
func (e *ElevenLabsTTS) Reset() error {
	e.reconnectMu.RLock()
	defer e.reconnectMu.RUnlock()

	e.mu.RLock()
	if !e.isInitialized {
		e.mu.RUnlock()
		return errors.New("service not initialized")
	}
	if e.conn == nil || e.currentSession == nil {
		e.mu.RUnlock()
		return errors.New("no active TTS session")
	}
	e.mu.RUnlock()

	// Close the connection abruptly to cancel current generation
	e.mu.Lock()
	e.closeConnectionLocked()
	e.mu.Unlock()

	// Reconnect with a fresh BOS for next generation
	conn, err := e.establishConnection()
	if err != nil {
		return fmt.Errorf("failed to reconnect after reset: %w", err)
	}
	if err := e.sendBOSLocked(conn); err != nil {
		conn.Close()
		return fmt.Errorf("failed to send BOS after reset: %w", err)
	}

	e.mu.Lock()
	e.conn = conn
	e.mu.Unlock()

	return nil
}

// sendJSON marshals and sends a JSON message over WebSocket
func (e *ElevenLabsTTS) sendJSON(conn *websocket.Conn, msg interface{}) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}
	e.logger.Infof("ElevenLabs TTS: sending message: %s", string(data))
	return conn.WriteMessage(websocket.TextMessage, data)
}

// heartbeat sends periodic pings to keep the connection alive
func (e *ElevenLabsTTS) heartbeat() {
	defer func() {
		e.mu.Lock()
		if e.heartbeatDone != nil {
			close(e.heartbeatDone)
			e.heartbeatDone = nil
		}
		e.mu.Unlock()
	}()

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			e.mu.RLock()
			conn := e.conn
			e.mu.RUnlock()

			if conn != nil {
				conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					e.logger.Infof("ElevenLabs TTS: heartbeat ping failed, closing connection: %v", err)
					e.mu.Lock()
					e.closeConnectionLocked()
					e.mu.Unlock()
				}
			}
		}
	}
}

// closeConnectionLocked safely closes the WebSocket connection (must be called with lock held)
func (e *ElevenLabsTTS) closeConnectionLocked() {
	if e.conn != nil {
		e.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		e.conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		e.conn.Close()
		e.conn = nil
	}
}

// cleanupSessionLocked clears session references (must be called with lock held)
func (e *ElevenLabsTTS) cleanupSessionLocked(session *ttsSession) {
	if session == nil {
		return
	}
	session.outChan = nil
	session.errorChan = nil
	session.doneChan = nil

	e.closeConnectionLocked()

	if e.currentSession == session {
		e.currentSession = nil
	}
}

// IsConnected returns whether there is an active WebSocket connection
func (e *ElevenLabsTTS) IsConnected() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.conn != nil && e.currentSession != nil
}
