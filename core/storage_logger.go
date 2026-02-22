package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// sessionLoggerKey is the context key for storing a per-session logger.
type sessionLoggerKey struct{}

// ContextWithSessionLogger returns a new context carrying the session logger.
func ContextWithSessionLogger(ctx context.Context, logger *Logger) context.Context {
	return context.WithValue(ctx, sessionLoggerKey{}, logger)
}

// SessionLoggerFromContext extracts the session logger from the context, or nil.
func SessionLoggerFromContext(ctx context.Context) *Logger {
	if l, ok := ctx.Value(sessionLoggerKey{}).(*Logger); ok {
		return l
	}
	return nil
}

// SessionMetadata is the first JSON line in each session log file.
type SessionMetadata struct {
	SessionID string `json:"session_id"`
	RoomName  string `json:"room_name,omitempty"`
	StartedAt string `json:"started_at"`
}

// LogEntry is a single JSON log line written after the metadata line.
type LogEntry struct {
	Timestamp string                 `json:"ts"`
	Level     string                 `json:"level"`
	Message   string                 `json:"msg"`
	Attrs     map[string]interface{} `json:"attrs,omitempty"`
}

// LogWriter abstracts the destination for session log entries.
// Implementations include SessionLogWriter (file) and WSLogWriter (WebSocket).
type LogWriter interface {
	Write(level, msg string, attrs map[string]interface{})
	Close()
}

// SessionLogWriter writes structured log lines to a per-session .jsonl file.
type SessionLogWriter struct {
	mu        sync.Mutex
	file      *os.File
	logDir    string
	sessionID string
}

// NewSessionLogWriter creates the log directory and session log file,
// writes the metadata first line, and creates an .active marker file.
func NewSessionLogWriter(logDir, sessionID, roomName string) (*SessionLogWriter, error) {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("storage logger: mkdir %q: %w", logDir, err)
	}

	filePath := filepath.Join(logDir, sessionID+".jsonl")
	f, err := os.Create(filePath)
	if err != nil {
		return nil, fmt.Errorf("storage logger: create %q: %w", filePath, err)
	}

	// Write metadata first line.
	meta := SessionMetadata{
		SessionID: sessionID,
		RoomName:  roomName,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.Marshal(meta)
	f.Write(data)
	f.Write([]byte("\n"))

	// Create .active marker.
	activePath := filepath.Join(logDir, sessionID+".active")
	if af, err := os.Create(activePath); err == nil {
		af.Close()
	}

	return &SessionLogWriter{
		file:      f,
		logDir:    logDir,
		sessionID: sessionID,
	}, nil
}

// Write appends a structured log line to the session file.
func (w *SessionLogWriter) Write(level, msg string, attrs map[string]interface{}) {
	entry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Level:     level,
		Message:   msg,
		Attrs:     attrs,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		w.file.Write(data)
		w.file.Write([]byte("\n"))
	}
}

// Close flushes and closes the log file, then removes the .active marker.
func (w *SessionLogWriter) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file != nil {
		w.file.Close()
		w.file = nil
	}

	activePath := filepath.Join(w.logDir, w.sessionID+".active")
	os.Remove(activePath)
}

// NewSessionLogger creates a Logger that tees output to both the base logger
// (console) and the provided LogWriter. All child loggers created via With()
// inherit this behaviour automatically.
func NewSessionLogger(baseLogger *Logger, writer LogWriter) *Logger {
	handler := func(level string, msg string, attrs map[string]interface{}) {
		// Console output via the base logger's handler.
		if baseLogger.handlerFunc != nil {
			baseLogger.handlerFunc(level, msg, attrs)
		}
		// Destination output (file, WebSocket, etc.).
		writer.Write(level, msg, attrs)
	}

	return &Logger{
		handlerFunc: handler,
		attrs:       make(map[string]interface{}),
	}
}
