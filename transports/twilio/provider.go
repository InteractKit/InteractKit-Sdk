package twilio

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	// Replace with your actual import path
	"interactkit/handlers/transport"

	"github.com/gorilla/websocket"
)

// TwilioTransportProvider implements ITransportProvider for Twilio WebSocket connections
type TwilioTransportProvider struct {
	config        *Config
	server        *http.Server
	upgrader      websocket.Upgrader
	jobHandler    func(svc transport.ITransportService, ctx context.Context) error
	mu            sync.RWMutex
	isRunning     bool
	connections   map[string]*TwilioTransportService
	connectionsMu sync.RWMutex
}

// NewTwilioTransportProvider creates a new Twilio transport provider
func NewTwilioTransportProvider(config *Config) *TwilioTransportProvider {
	if config == nil {
		config = DefaultConfig()
	}

	upgrader := websocket.Upgrader{
		ReadBufferSize:  config.ReadBufferSize,
		WriteBufferSize: config.WriteBufferSize,
		CheckOrigin: func(r *http.Request) bool {
			return true // Allow all origins for Twilio webhooks
		},
	}

	return &TwilioTransportProvider{
		config:      config,
		upgrader:    upgrader,
		connections: make(map[string]*TwilioTransportService),
	}
}

// Start implements ITransportProvider.Start
func (p *TwilioTransportProvider) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.isRunning {
		return fmt.Errorf("provider already running")
	}

	mux := http.NewServeMux()
	mux.HandleFunc(p.config.Path, p.handleWebSocket)

	addr := fmt.Sprintf(":%d", p.config.Port)
	p.server = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		var err error
		if p.config.EnableTLS {
			err = p.server.ListenAndServeTLS(p.config.TLSCertFile, p.config.TLSKeyFile)
		} else {
			err = p.server.ListenAndServe()
		}

		if err != nil && err != http.ErrServerClosed {
			fmt.Printf("Twilio provider server error: %v\n", err)
		}
	}()

	p.isRunning = true
	fmt.Printf("Twilio WebSocket provider started on port %d, path: %s\n",
		p.config.Port, p.config.Path)

	return nil
}

// Stop implements ITransportProvider.Stop
func (p *TwilioTransportProvider) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.isRunning {
		return nil
	}

	// Close all active connections
	p.connectionsMu.Lock()
	for _, conn := range p.connections {
		conn.Close()
	}
	p.connections = make(map[string]*TwilioTransportService)
	p.connectionsMu.Unlock()

	// Shutdown HTTP server
	if p.server != nil {
		err := p.server.Shutdown(context.Background())
		if err != nil {
			return fmt.Errorf("error shutting down server: %w", err)
		}
	}

	p.isRunning = false
	return nil
}

// RegisterJobHandler implements ITransportProvider.RegisterJobHandler
func (p *TwilioTransportProvider) RegisterJobHandler(
	handler func(svc transport.ITransportService, ctx context.Context) error,
) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if handler == nil {
		return fmt.Errorf("handler cannot be nil")
	}

	p.jobHandler = handler
	return nil
}

// handleWebSocket handles incoming WebSocket connections from Twilio
func (p *TwilioTransportProvider) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Upgrade HTTP connection to WebSocket
	conn, err := p.upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Printf("Failed to upgrade connection: %v\n", err)
		return
	}

	// Set max message size
	conn.SetReadLimit(p.config.MaxMessageSize)

	// Create transport service
	transport := NewTwilioTransportService(conn, p.config)

	// Store connection
	p.connectionsMu.Lock()
	// Use connection ID as key - in production, use a proper ID
	p.connections[fmt.Sprintf("%p", conn)] = transport
	p.connectionsMu.Unlock()

	// Clean up on disconnect
	defer func() {
		p.connectionsMu.Lock()
		delete(p.connections, fmt.Sprintf("%p", conn))
		p.connectionsMu.Unlock()
	}()

	// Execute job handler if registered
	if p.jobHandler != nil {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err = p.jobHandler(transport, ctx)
		if err != nil {
			fmt.Printf("Job handler error: %v\n", err)
		}
	}
}

// GetActiveConnections returns the number of active WebSocket connections
func (p *TwilioTransportProvider) GetActiveConnections() int {
	p.connectionsMu.RLock()
	defer p.connectionsMu.RUnlock()
	return len(p.connections)
}
