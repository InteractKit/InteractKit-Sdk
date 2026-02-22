package daily

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"interactkit/core"
	"interactkit/handlers/transport"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// DailyTransportProvider implements transport.ITransportProvider for Daily.co.
type DailyTransportProvider struct {
	config     *Config
	logger     *core.Logger
	apiClient  *DailyAPIClient
	server     *http.Server
	upgrader   websocket.Upgrader
	jobHandler func(svc transport.ITransportService, ctx context.Context) error

	mu            sync.RWMutex
	isRunning     bool
	connections   map[string]*DailyTransportService
	connectionsMu sync.RWMutex
}

// NewDailyTransportProvider creates a new Daily transport provider.
func NewDailyTransportProvider(config *Config, logger *core.Logger) (*DailyTransportProvider, error) {
	if config == nil {
		config = DefaultConfig()
	}
	if config.APIKey == "" {
		return nil, fmt.Errorf("Daily API key is required")
	}
	if logger == nil {
		logger = core.GetLogger()
	}

	apiClient := NewDailyAPIClient(config.APIKey, config.APIBaseURL)

	upgrader := websocket.Upgrader{
		ReadBufferSize:  config.ReadBufferSize,
		WriteBufferSize: config.WriteBufferSize,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	return &DailyTransportProvider{
		config:      config,
		logger:      logger.With(map[string]interface{}{"component": "daily-provider"}),
		apiClient:   apiClient,
		upgrader:    upgrader,
		connections: make(map[string]*DailyTransportService),
	}, nil
}

// RegisterJobHandler implements transport.ITransportProvider.
func (p *DailyTransportProvider) RegisterJobHandler(
	handler func(svc transport.ITransportService, ctx context.Context) error,
) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if handler == nil {
		return fmt.Errorf("handler cannot be nil")
	}
	p.jobHandler = handler
	p.logger.Info("job handler registered")
	return nil
}

// Start implements transport.ITransportProvider.
func (p *DailyTransportProvider) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.isRunning {
		return fmt.Errorf("provider already running")
	}

	mux := http.NewServeMux()
	mux.HandleFunc(p.config.Path, p.handleWebSocket)
	mux.HandleFunc("/daily-session", p.handleCreateSession)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "provider": "daily"})
	})

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
			p.logger.With(map[string]interface{}{"error": err}).Error("server error")
		}
	}()

	p.isRunning = true
	p.logger.With(map[string]interface{}{
		"port": p.config.Port,
		"path": p.config.Path,
	}).Info("Daily transport provider started")

	return nil
}

// Stop implements transport.ITransportProvider.
func (p *DailyTransportProvider) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.isRunning {
		return nil
	}

	p.connectionsMu.Lock()
	for _, conn := range p.connections {
		conn.Close()
	}
	p.connections = make(map[string]*DailyTransportService)
	p.connectionsMu.Unlock()

	if p.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := p.server.Shutdown(ctx); err != nil {
			return fmt.Errorf("error shutting down server: %w", err)
		}
	}

	p.isRunning = false
	p.logger.Info("Daily transport provider stopped")
	return nil
}

// handleCreateSession creates a Daily room and returns connection details.
func (p *DailyTransportProvider) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	roomName := p.config.RoomName
	if roomName == "" {
		roomName = fmt.Sprintf("interactkit-%s", uuid.New().String()[:8])
	}

	room, err := p.apiClient.CreateRoom(RoomConfig{
		Name:    roomName,
		Privacy: "private",
		Properties: &RoomProperties{
			MaxParticipants: p.config.MaxParticipants,
			ExpiresAt:       time.Now().Add(time.Duration(p.config.ExpirySeconds) * time.Second).Unix(),
			StartVideoOff:   true,
		},
	})
	if err != nil {
		p.logger.With(map[string]interface{}{"error": err}).Error("failed to create room")
		http.Error(w, fmt.Sprintf("failed to create room: %v", err), http.StatusInternalServerError)
		return
	}

	botToken, err := p.apiClient.CreateMeetingToken(MeetingTokenConfig{
		Properties: &MeetingTokenProperties{
			RoomName:  room.Name,
			UserName:  p.config.BotName,
			IsOwner:   p.config.IsOwner,
			ExpiresAt: time.Now().Add(time.Duration(p.config.ExpirySeconds) * time.Second).Unix(),
		},
	})
	if err != nil {
		p.logger.With(map[string]interface{}{"error": err}).Error("failed to create bot token")
		http.Error(w, fmt.Sprintf("failed to create token: %v", err), http.StatusInternalServerError)
		return
	}

	userToken, err := p.apiClient.CreateMeetingToken(MeetingTokenConfig{
		Properties: &MeetingTokenProperties{
			RoomName:  room.Name,
			UserName:  "user",
			ExpiresAt: time.Now().Add(time.Duration(p.config.ExpirySeconds) * time.Second).Unix(),
		},
	})
	if err != nil {
		p.logger.With(map[string]interface{}{"error": err}).Error("failed to create user token")
		http.Error(w, fmt.Sprintf("failed to create user token: %v", err), http.StatusInternalServerError)
		return
	}

	p.logger.With(map[string]interface{}{
		"room": room.Name,
		"url":  room.URL,
	}).Info("created Daily session")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"room_name":  room.Name,
		"room_url":   room.URL,
		"bot_token":  botToken,
		"user_token": userToken,
		"ws_url":     fmt.Sprintf("ws://localhost:%d%s?room=%s", p.config.Port, p.config.Path, room.Name),
	})
}

// handleWebSocket handles incoming WebSocket connections from a Daily client bridge.
func (p *DailyTransportProvider) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := p.upgrader.Upgrade(w, r, nil)
	if err != nil {
		p.logger.With(map[string]interface{}{"error": err}).Error("failed to upgrade connection")
		return
	}

	conn.SetReadLimit(p.config.MaxMessageSize)

	roomName := r.URL.Query().Get("room")
	if roomName == "" {
		roomName = fmt.Sprintf("unknown-%s", uuid.New().String()[:8])
	}

	sessionID := fmt.Sprintf("daily-%s-%s", roomName, uuid.New().String()[:8])

	// Create a per-session logger that tees to console + file.
	logDir := os.Getenv("LOG_DIR")
	if logDir == "" {
		logDir = "logs"
	}
	var sessionLogger *core.Logger
	logWriter, slErr := core.NewSessionLogWriter(logDir, sessionID, roomName)
	if slErr != nil {
		p.logger.With(map[string]interface{}{"error": slErr}).Warn("failed to create session logger, using default")
		sessionLogger = p.logger
	} else {
		sessionLogger = core.NewSessionLogger(p.logger, logWriter)
	}
	if logWriter != nil {
		defer logWriter.Close()
	}

	jobLogger := sessionLogger.With(map[string]interface{}{"session": sessionID, "room": roomName})

	svc := NewDailyTransportService(conn, p.config, roomName, jobLogger)
	connID := fmt.Sprintf("%s-%p", roomName, conn)

	p.connectionsMu.Lock()
	p.connections[connID] = svc
	p.connectionsMu.Unlock()

	defer func() {
		p.connectionsMu.Lock()
		delete(p.connections, connID)
		p.connectionsMu.Unlock()
	}()

	if p.jobHandler != nil {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		ctx = core.ContextWithSessionLogger(ctx, jobLogger)

		jobLogger.Info("starting job for Daily session")

		if err := p.jobHandler(svc, ctx); err != nil {
			jobLogger.With(map[string]interface{}{"error": err}).Error("job handler error")
		}
	}
}

// GetActiveConnections returns the number of active connections.
func (p *DailyTransportProvider) GetActiveConnections() int {
	p.connectionsMu.RLock()
	defer p.connectionsMu.RUnlock()
	return len(p.connections)
}
