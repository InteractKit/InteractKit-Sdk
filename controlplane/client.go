package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"interactkit/core"
	"interactkit/protocol"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultHeartbeatInterval = 5 * time.Second
	defaultSendBufferSize    = 256
	writeTimeout             = 10 * time.Second
)

// ClientConfig configures the control plane WebSocket client.
type ClientConfig struct {
	ConnectURL        string
	AgentID           string
	Version           string
	Metadata          map[string]string
	HeartbeatInterval time.Duration
	Logger            *core.Logger
}

// Client is the agent-side WebSocket client that connects outward to the UI
// server's control plane. It sends logs, status, heartbeats, and pipeline
// events, and receives config updates and control commands.
type Client struct {
	config ClientConfig
	conn   *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc
	logger *core.Logger

	// Callbacks set by the agent.
	OnConfigUpdate    func(settings json.RawMessage, keys map[string]string)
	OnRestartPipeline func()
	OnShutdown        func(reason string)

	sendCh chan []byte
	done   chan struct{}
	once   sync.Once
}

// NewClient creates a new control plane client.
func NewClient(cfg ClientConfig) *Client {
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = defaultHeartbeatInterval
	}
	if cfg.Logger == nil {
		cfg.Logger = core.GetLogger()
	}
	return &Client{
		config: cfg,
		logger: cfg.Logger.With(map[string]interface{}{"component": "controlplane"}),
		sendCh: make(chan []byte, defaultSendBufferSize),
		done:   make(chan struct{}),
	}
}

// Connect dials the UI server WebSocket endpoint, sends the registration
// message, and starts the read/write/heartbeat loops. The provided context
// controls the client's lifetime — cancelling it will close the connection.
func (c *Client) Connect(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx)

	c.logger.With(map[string]interface{}{"url": c.config.ConnectURL}).Info("connecting to control plane")

	conn, _, err := websocket.DefaultDialer.DialContext(c.ctx, c.config.ConnectURL, nil)
	if err != nil {
		c.cancel()
		return fmt.Errorf("controlplane: dial %q: %w", c.config.ConnectURL, err)
	}
	c.conn = conn

	// Send registration.
	reg := protocol.RegisterPayload{
		AgentID:   c.config.AgentID,
		Version:   c.config.Version,
		Metadata:  c.config.Metadata,
		Timestamp: time.Now().UTC(),
	}
	if err := c.send(protocol.MsgRegister, reg); err != nil {
		conn.Close()
		c.cancel()
		return fmt.Errorf("controlplane: send register: %w", err)
	}

	c.logger.With(map[string]interface{}{"agent_id": c.config.AgentID}).Info("registered with control plane")

	go c.readLoop()
	go c.writeLoop()
	go c.heartbeatLoop()

	return nil
}

// SendLog sends a log entry for a session to the UI.
func (c *Client) SendLog(sessionID string, entry protocol.LogEntry) {
	payload := protocol.LogPayload{
		AgentID:   c.config.AgentID,
		SessionID: sessionID,
		Entry:     entry,
	}
	c.enqueue(protocol.MsgLog, payload)
}

// SendStatus sends an agent-level status update.
func (c *Client) SendStatus(status string, sessions []protocol.SessionInfo) {
	payload := protocol.StatusPayload{
		AgentID:  c.config.AgentID,
		Status:   status,
		Sessions: sessions,
	}
	c.enqueue(protocol.MsgStatus, payload)
}

// SendEvent sends a pipeline event.
func (c *Client) SendEvent(sessionID, eventID string, data json.RawMessage) {
	payload := protocol.EventPayload{
		AgentID:   c.config.AgentID,
		SessionID: sessionID,
		EventID:   eventID,
		Data:      data,
	}
	c.enqueue(protocol.MsgEvent, payload)
}

// SendLogEnd signals that a session's log stream has ended.
func (c *Client) SendLogEnd(sessionID string) {
	payload := protocol.LogEndPayload{
		AgentID:   c.config.AgentID,
		SessionID: sessionID,
	}
	c.enqueue(protocol.MsgLogEnd, payload)
}

// Wait blocks until the connection drops or the context is cancelled.
func (c *Client) Wait() error {
	<-c.done
	return nil
}

// Close shuts down the client.
func (c *Client) Close() {
	c.once.Do(func() {
		c.cancel()
		if c.conn != nil {
			c.conn.Close()
		}
	})
}

func (c *Client) send(msgType protocol.MessageType, payload interface{}) error {
	data, err := protocol.Marshal(msgType, payload)
	if err != nil {
		return err
	}
	c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

func (c *Client) enqueue(msgType protocol.MessageType, payload interface{}) {
	data, err := protocol.Marshal(msgType, payload)
	if err != nil {
		c.logger.With(map[string]interface{}{"error": err, "type": string(msgType)}).Warn("failed to marshal message, dropping")
		return
	}
	select {
	case c.sendCh <- data:
	default:
		// Buffer full — drop oldest and push new.
		select {
		case <-c.sendCh:
		default:
		}
		select {
		case c.sendCh <- data:
		default:
		}
	}
}

func (c *Client) readLoop() {
	defer func() {
		c.once.Do(func() { close(c.done) })
		c.cancel()
	}()

	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				c.logger.With(map[string]interface{}{"error": err}).Warn("control plane connection lost")
			}
			return
		}

		msgType, payload, err := protocol.Unmarshal(data)
		if err != nil {
			c.logger.With(map[string]interface{}{"error": err}).Warn("invalid message from control plane")
			continue
		}

		switch msgType {
		case protocol.MsgConfigUpdate:
			if c.OnConfigUpdate != nil {
				p, err := protocol.UnmarshalPayload[protocol.ConfigUpdatePayload](payload)
				if err != nil {
					c.logger.With(map[string]interface{}{"error": err}).Warn("invalid config_update payload")
					continue
				}
				c.OnConfigUpdate(p.Settings, p.Keys)
			}

		case protocol.MsgRestartPipeline:
			if c.OnRestartPipeline != nil {
				c.OnRestartPipeline()
			}

		case protocol.MsgShutdown:
			p, _ := protocol.UnmarshalPayload[protocol.ShutdownPayload](payload)
			reason := p.Reason
			if reason == "" {
				reason = "shutdown requested by control plane"
			}
			c.logger.With(map[string]interface{}{"reason": reason}).Info("shutdown requested")
			if c.OnShutdown != nil {
				c.OnShutdown(reason)
			}
			return

		default:
			c.logger.With(map[string]interface{}{"type": string(msgType)}).Warn("unknown message type from control plane")
		}
	}
}

func (c *Client) writeLoop() {
	for {
		select {
		case data := <-c.sendCh:
			c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				c.logger.With(map[string]interface{}{"error": err}).Warn("write to control plane failed")
				return
			}
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Client) heartbeatLoop() {
	ticker := time.NewTicker(c.config.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			hb := protocol.HeartbeatPayload{
				AgentID:   c.config.AgentID,
				Timestamp: time.Now().UTC(),
				Status:    "idle",
			}
			c.enqueue(protocol.MsgHeartbeat, hb)
		case <-c.ctx.Done():
			return
		}
	}
}
