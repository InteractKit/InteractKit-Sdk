package protocol

import (
	"encoding/json"
	"time"
)

// MessageType enumerates all control-plane message types.
type MessageType string

const (
	// Agent -> UI
	MsgRegister  MessageType = "register"
	MsgHeartbeat MessageType = "heartbeat"
	MsgLog       MessageType = "log"
	MsgStatus    MessageType = "status"
	MsgEvent     MessageType = "event"
	MsgLogEnd    MessageType = "log_end"

	// UI -> Agent
	MsgConfigUpdate    MessageType = "config_update"
	MsgRestartPipeline MessageType = "restart_pipeline"
	MsgShutdown        MessageType = "shutdown"
	MsgAck             MessageType = "ack"
)

// Envelope is the outer JSON wrapper for all WebSocket messages.
type Envelope struct {
	Type    MessageType     `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// --- Agent -> UI payloads ---

// RegisterPayload is sent once by the agent immediately after connecting.
type RegisterPayload struct {
	AgentID      string            `json:"agent_id"`
	Version      string            `json:"version,omitempty"`
	Capabilities []string          `json:"capabilities,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	Timestamp    time.Time         `json:"timestamp"`
}

// HeartbeatPayload is sent periodically to keep the connection alive.
type HeartbeatPayload struct {
	AgentID        string    `json:"agent_id"`
	Timestamp      time.Time `json:"timestamp"`
	ActiveSessions int       `json:"active_sessions"`
	Status         string    `json:"status"` // "idle", "running", "error"
}

// LogPayload carries a single log entry from a session.
type LogPayload struct {
	AgentID   string   `json:"agent_id"`
	SessionID string   `json:"session_id"`
	Entry     LogEntry `json:"entry"`
}

// LogEntry is a structured log line.
type LogEntry struct {
	Timestamp string                 `json:"ts"`
	Level     string                 `json:"level"`
	Message   string                 `json:"msg"`
	Attrs     map[string]interface{} `json:"attrs,omitempty"`
}

// StatusPayload carries agent-level status with active sessions.
type StatusPayload struct {
	AgentID  string        `json:"agent_id"`
	Status   string        `json:"status"` // "idle", "running", "error", "draining"
	Sessions []SessionInfo `json:"sessions"`
}

// SessionInfo describes a single active or completed session.
type SessionInfo struct {
	SessionID string `json:"session_id"`
	RoomName  string `json:"room_name,omitempty"`
	StartedAt string `json:"started_at"`
	Status    string `json:"status"` // "active", "completed"
}

// EventPayload carries a pipeline event for external consumers.
type EventPayload struct {
	AgentID   string          `json:"agent_id"`
	SessionID string          `json:"session_id,omitempty"`
	EventID   string          `json:"event_id"`
	Data      json.RawMessage `json:"data"`
}

// LogEndPayload signals that a session's log stream has ended.
type LogEndPayload struct {
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
}

// --- UI -> Agent payloads ---

// ConfigUpdatePayload pushes new configuration to the agent.
type ConfigUpdatePayload struct {
	Settings json.RawMessage   `json:"settings,omitempty"`
	Keys     map[string]string `json:"keys,omitempty"`
}

// RestartPipelinePayload requests the agent to restart its pipeline.
type RestartPipelinePayload struct {
	Reason string `json:"reason,omitempty"`
}

// ShutdownPayload requests the agent to shut down gracefully.
type ShutdownPayload struct {
	Reason       string `json:"reason,omitempty"`
	GraceSeconds int    `json:"grace_seconds,omitempty"`
}

// AckPayload acknowledges a received message.
type AckPayload struct {
	AckedType MessageType `json:"acked_type"`
	OK        bool        `json:"ok"`
	Error     string      `json:"error,omitempty"`
}
