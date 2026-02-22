package controlplane

import (
	"interactkit/protocol"
	"time"
)

// WSLogWriter implements core.LogWriter by sending log entries over the
// control plane WebSocket instead of writing to disk.
type WSLogWriter struct {
	client    *Client
	sessionID string
}

// NewWSLogWriter creates a LogWriter that routes logs to the control plane.
func NewWSLogWriter(client *Client, sessionID string) *WSLogWriter {
	return &WSLogWriter{
		client:    client,
		sessionID: sessionID,
	}
}

// Write sends a log entry over the WebSocket.
func (w *WSLogWriter) Write(level, msg string, attrs map[string]interface{}) {
	entry := protocol.LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Level:     level,
		Message:   msg,
		Attrs:     attrs,
	}
	w.client.SendLog(w.sessionID, entry)
}

// Close signals the end of the session's log stream.
func (w *WSLogWriter) Close() {
	w.client.SendLogEnd(w.sessionID)
}
