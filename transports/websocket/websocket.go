package websocket

import (
	"encoding/json"
	"interactkit/core"
	"sync"

	"github.com/gorilla/websocket"
)

// WebSocketService implements core.TransportService
type WebSocketService struct {
	conn    *websocket.Conn
	mu      sync.Mutex // protects writes
	started bool
	done    chan struct{} // signals goroutines to stop
}

// NewWebSocketService creates a new WebSocketService with an existing connection
func NewWebSocketService(conn *websocket.Conn) *WebSocketService {
	return &WebSocketService{
		conn:    conn,
		started: true,
	}
}

// Connect is a no-op since connection is already provided
func (ws *WebSocketService) Connect() error {
	if ws.conn == nil {
		return websocket.ErrCloseSent
	}
	ws.started = true
	return nil
}

// SendRawOutput implements core.TransportService - sends MediaChunk to WebSocket client
func (ws *WebSocketService) SendRawOutput(chunk core.MediaChunk) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ws.conn == nil {
		return websocket.ErrCloseSent
	}

	// Check what type of data we have and send appropriately
	if chunk.Text.Text != "" {
		// Send text as JSON or raw text
		// We'll send as JSON to maintain structure
		data := map[string]interface{}{
			"type": "text",
			"data": chunk.Text.Text,
		}
		jsonData, err := json.Marshal(data)
		if err != nil {
			return err
		}
		return ws.conn.WriteMessage(websocket.TextMessage, jsonData)
	}

	if chunk.Audio.Data != nil && len(*chunk.Audio.Data) > 0 {
		// Send audio as binary with metadata in separate message or header?
		// For simplicity, we'll send as binary with a JSON header first
		header := map[string]interface{}{
			"type":       "audio",
			"sampleRate": chunk.Audio.SampleRate,
			"channels":   chunk.Audio.Channels,
			"format":     chunk.Audio.Format,
			"size":       len(*chunk.Audio.Data),
		}

		// Send header as text message
		headerData, _ := json.Marshal(header)
		if err := ws.conn.WriteMessage(websocket.TextMessage, headerData); err != nil {
			return err
		}

		// Send audio data as binary
		return ws.conn.WriteMessage(websocket.BinaryMessage, *chunk.Audio.Data)
	}

	if chunk.Video.Data != nil && len(*chunk.Video.Data) > 0 {
		// Send video as binary with metadata
		header := map[string]interface{}{
			"type":       "video",
			"frameRate":  chunk.Video.FrameRate,
			"resolution": chunk.Video.Resolution,
			"format":     chunk.Video.Format,
			"size":       len(*chunk.Video.Data),
		}

		headerData, _ := json.Marshal(header)
		if err := ws.conn.WriteMessage(websocket.TextMessage, headerData); err != nil {
			return err
		}

		return ws.conn.WriteMessage(websocket.BinaryMessage, *chunk.Video.Data)
	}

	return nil
}

// StartReceiving continuously reads messages from the WebSocket connection
// and converts them to core.MediaChunk
func (ws *WebSocketService) StartReceiving(outputChan chan<- core.MediaChunk, errorChan chan<- error) {
	if ws.conn == nil {
		errorChan <- websocket.ErrCloseSent
		return
	}

	go func() {
		for ws.started {
			messageType, msg, err := ws.conn.ReadMessage()
			if err != nil {
				errorChan <- err
				return
			}

			chunk := core.MediaChunk{}

			if messageType == websocket.TextMessage {
				// Try to parse as JSON first to see if it's structured data
				var data map[string]interface{}
				if err := json.Unmarshal(msg, &data); err == nil {
					// It's JSON, check the type
					if msgType, ok := data["type"].(string); ok {
						switch msgType {
						case "text":
							if text, ok := data["data"].(string); ok {
								chunk.Text = core.TextChunk{Text: text}
							}
						case "audio":
							// This is just a header, actual audio data will come as binary
							// Store metadata for next binary message
							continue
						case "video":
							// This is just a header, actual video data will come as binary
							// Store metadata for next binary message
							continue
						}
					}
				} else {
					// Plain text message
					chunk.Text = core.TextChunk{Text: string(msg)}
				}
			} else if messageType == websocket.BinaryMessage {
				// Binary message - assume it's audio or video
				// For now, treat as audio with default settings
				chunk.Audio = core.AudioChunk{
					Data:       &msg,
					SampleRate: 24000, // Default
					Channels:   1,     // Default
					Format:     core.PCM,
				}
			}

			// Only send if we actually have data
			if chunk.Text.Text != "" ||
				(chunk.Audio.Data != nil && len(*chunk.Audio.Data) > 0) ||
				(chunk.Video.Data != nil && len(*chunk.Video.Data) > 0) {
				outputChan <- chunk
			}
		}
	}()
}

// Close shuts down the WebSocket connection
func (ws *WebSocketService) Close() error {
	ws.started = false
	if ws.conn != nil {
		return ws.conn.Close()
	}
	return nil
}
