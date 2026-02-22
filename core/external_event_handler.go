package core

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// WireEvent is the JSON envelope used on the WebSocket connection.
//
//	{"id": "<event id>", "payload": { /* event-specific fields */ }}
type WireEvent struct {
	ID      string          `json:"id"`
	Payload json.RawMessage `json:"payload"`
}

// ExternalEventHandler is a WebSocket server (port 19304) that bridges the
// pipeline with external systems.
//
//   - IExternalOutputEvent packets emitted by any pipeline handler are
//     serialised as WireEvent and broadcast to every connected client.
//
//   - Incoming WireEvent messages are deserialised using a registered factory
//     and injected into the pipeline top as IExternalInputEvent, so every
//     handler in the chain receives them.
type ExternalEventHandler struct {
	logger        *Logger
	outputTopChan chan<- *EventPacket
	ctx           context.Context

	upgrader  websocket.Upgrader
	clients   map[*websocket.Conn]struct{}
	clientsMu sync.RWMutex

	inputRegistry map[string]func() IExternalInputEvent
	registryMu    sync.RWMutex
}

func NewExternalEventHandler(logger *Logger) *ExternalEventHandler {
	if logger == nil {
		logger = GetLogger()
	}
	return &ExternalEventHandler{
		logger:        logger,
		clients:       make(map[*websocket.Conn]struct{}),
		inputRegistry: make(map[string]func() IExternalInputEvent),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// Initialize wires the handler to the pipeline and starts the WebSocket server.
func (e *ExternalEventHandler) Initialize(
	outputTopChan chan<- *EventPacket,
	ctx context.Context,
) {
	e.outputTopChan = outputTopChan
	e.ctx = ctx
	go e.serve()
}

// Broadcast serialises an IExternalOutputEvent and sends it to all connected
// WebSocket clients. Called directly by the Runner when it detects an
// IExternalOutputEvent exiting the pipeline.
func (e *ExternalEventHandler) Broadcast(packet *EventPacket) {
	ev, ok := packet.Event.(IExternalOutputEvent)
	if !ok {
		return
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		e.logger.Errorf("ExternalEventHandler: marshal output event %q: %v", ev.GetId(), err)
		return
	}
	wire, err := json.Marshal(WireEvent{ID: ev.GetId(), Payload: json.RawMessage(payload)})
	if err != nil {
		return
	}
	e.broadcast(wire)
}

// RegisterInputEvent registers a factory for a given event ID.  When a
// WebSocket client sends {"id": id, "payload": {...}}, the factory is called to
// create a zero-value event, the payload is unmarshalled into it, and the event
// is pushed to the pipeline top.
func (e *ExternalEventHandler) RegisterInputEvent(id string, factory func() IExternalInputEvent) {
	e.registryMu.Lock()
	defer e.registryMu.Unlock()
	e.inputRegistry[id] = factory
}

// SendInput injects an IExternalInputEvent directly into the pipeline top
// without going through the WebSocket layer.
func (e *ExternalEventHandler) SendInput(event IExternalInputEvent, relayer string) {
	packet := NewEventPacket(event, EventRelayDestinationTopService, relayer)
	select {
	case e.outputTopChan <- packet:
	case <-e.ctx.Done():
	}
}

func (e *ExternalEventHandler) broadcast(data []byte) {
	e.clientsMu.RLock()
	conns := make([]*websocket.Conn, 0, len(e.clients))
	for conn := range e.clients {
		conns = append(conns, conn)
	}
	e.clientsMu.RUnlock()

	for _, conn := range conns {
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			e.logger.Errorf("ExternalEventHandler: write to client: %v", err)
		}
	}
}

func (e *ExternalEventHandler) serve() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", e.handleWS)
	server := &http.Server{Addr: ":19304", Handler: mux}

	go func() {
		<-e.ctx.Done()
		_ = server.Shutdown(context.Background())
	}()

	e.logger.Infof("ExternalEventHandler WebSocket server listening on :19304")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		e.logger.Errorf("ExternalEventHandler WebSocket server: %v", err)
	}
}

func (e *ExternalEventHandler) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := e.upgrader.Upgrade(w, r, nil)
	if err != nil {
		e.logger.Errorf("ExternalEventHandler: upgrade: %v", err)
		return
	}
	defer conn.Close()

	e.clientsMu.Lock()
	e.clients[conn] = struct{}{}
	e.clientsMu.Unlock()

	defer func() {
		e.clientsMu.Lock()
		delete(e.clients, conn)
		e.clientsMu.Unlock()
	}()

	e.logger.Infof("ExternalEventHandler: client connected (%s)", conn.RemoteAddr())

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var wire WireEvent
		if err := json.Unmarshal(data, &wire); err != nil {
			e.logger.Errorf("ExternalEventHandler: unmarshal wire event: %v", err)
			continue
		}

		e.registryMu.RLock()
		factory, ok := e.inputRegistry[wire.ID]
		e.registryMu.RUnlock()
		if !ok {
			e.logger.Errorf("ExternalEventHandler: no factory registered for event id %q", wire.ID)
			continue
		}

		ev := factory()
		if err := json.Unmarshal(wire.Payload, ev); err != nil {
			e.logger.Errorf("ExternalEventHandler: unmarshal payload for %q: %v", wire.ID, err)
			continue
		}

		e.SendInput(ev, "external-ws")
	}
}
