package twilio

import (
	"fmt"
	"sync"
	"time"

	"interactkit/core" // Replace with your actual import path

	"github.com/gorilla/websocket"
)

// TwilioMediaMessage represents a message from Twilio's media stream
type TwilioMediaMessage struct {
	Event     string `json:"event"`
	StreamSid string `json:"streamSid,omitempty"`
	Sequence  string `json:"sequence,omitempty"`
	Media     struct {
		Track     string `json:"track"`
		Payload   string `json:"payload"`
		Timestamp string `json:"timestamp"`
	} `json:"media,omitempty"`
	Start struct {
		AccountSid string `json:"accountSid"`
		StreamSid  string `json:"streamSid"`
		CallSid    string `json:"callSid"`
	} `json:"start,omitempty"`
}

// TwilioTransportService implements ITransportService for Twilio WebSocket connections
type TwilioTransportService struct {
	core.IService
	conn      *websocket.Conn
	config    *Config
	streamSid string
	callSid   string
	mu        sync.RWMutex
	connected bool
}

// NewTwilioTransportService creates a new Twilio transport service
func NewTwilioTransportService(conn *websocket.Conn, config *Config) *TwilioTransportService {
	return &TwilioTransportService{
		conn:      conn,
		config:    config,
		connected: true,
	}
}

// Connect implements ITransportService.Connect
func (t *TwilioTransportService) Connect() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.connected {
		return nil
	}

	// Reconnection logic would go here
	t.connected = true
	return nil
}

// SendEvent implements ITransportService.SendEvent
func (t *TwilioTransportService) SendEvent(data core.IEvent) error {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if !t.connected {
		return fmt.Errorf("transport service not connected")
	}

	// Convert event to Twilio format
	message := map[string]interface{}{
		"event":     "media",
		"streamSid": t.streamSid,
		"media": map[string]interface{}{
			"payload":   data, // This would need proper encoding
			"timestamp": time.Now().UnixMilli(),
		},
	}

	return t.conn.WriteJSON(message)
}

// SendAudio sends audio data to Twilio
func (t *TwilioTransportService) SendAudio(audioData []byte) error {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if !t.connected {
		return fmt.Errorf("transport service not connected")
	}

	message := map[string]interface{}{
		"event":     "media",
		"streamSid": t.streamSid,
		"media": map[string]interface{}{
			"payload":   audioData,
			"timestamp": time.Now().UnixMilli(),
		},
	}

	return t.conn.WriteJSON(message)
}

// StartReceiving implements ITransportService.StartReceiving
func (t *TwilioTransportService) StartReceiving(outputChan chan<- core.MediaChunk, errorChan chan<- error) {
	defer t.Close()

	for {
		var msg TwilioMediaMessage
		err := t.conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				errorChan <- fmt.Errorf("websocket error: %w", err)
			}
			break
		}

		switch msg.Event {
		case "start":
			t.handleStartMessage(&msg)
		case "media":
			t.handleMediaMessage(&msg, outputChan)
		case "stop":
			t.handleStopMessage()
			return
		}
	}
}

// handleStartMessage processes the start event from Twilio
func (t *TwilioTransportService) handleStartMessage(msg *TwilioMediaMessage) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.streamSid = msg.Start.StreamSid
	t.callSid = msg.Start.CallSid
}

// handleMediaMessage processes media events from Twilio
func (t *TwilioTransportService) handleMediaMessage(msg *TwilioMediaMessage, outputChan chan<- core.MediaChunk) {
	// Decode base64 audio data
	audioData := []byte(msg.Media.Payload) // In production, properly decode base64

	// Create audio chunk
	audioChunk := core.AudioChunk{
		Data:       &audioData,
		SampleRate: t.config.AudioSampleRate,
		Channels:   1,         // Twilio sends mono audio
		Format:     core.ULAW, // Twilio uses μ-law encoding
		Timestamp:  time.Now(),
	}

	outputChan <- core.MediaChunk{
		Audio: audioChunk,
	}
}

// handleStopMessage processes the stop event from Twilio
func (t *TwilioTransportService) handleStopMessage() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.connected = false
}

// Close closes the WebSocket connection
func (t *TwilioTransportService) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.connected {
		return nil
	}

	t.connected = false
	return t.conn.Close()
}

// GetStreamSid returns the current stream SID
func (t *TwilioTransportService) GetStreamSid() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.streamSid
}

// GetCallSid returns the current call SID
func (t *TwilioTransportService) GetCallSid() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.callSid
}
