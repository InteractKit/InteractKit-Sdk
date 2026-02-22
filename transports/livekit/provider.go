package livekit

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"interactkit/core"
	"interactkit/handlers/transport"
	"net"
	"os"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"google.golang.org/protobuf/proto"
)

const (
	DefaultDrainTimeout    = 30 * time.Minute
	DefaultShutdownTimeout = 10 * time.Second
	MaxReconnectDelay      = 30 * time.Second
	InitialReconnectDelay  = 1 * time.Second
	MaxReconnectAttempts   = 0 // 0 = unlimited
)

type State int

const (
	StateDisconnected State = iota
	StateConnecting
	StateConnected
	StateDraining
)

type Config struct {
	URL          string
	APIKey       string
	APISecret    string
	AgentName    string
	Version      string
	MaxJobs      uint32
	DevMode      bool
	Logger       *core.Logger
	DrainTimeout time.Duration

	// Optional
	JobTypes    []livekit.JobType
	HTTPPort    int
	Permissions *livekit.ParticipantPermission
}

func DefaultConfig() Config {
	return Config{
		AgentName:    "",
		Version:      "1.0.0",
		JobTypes:     []livekit.JobType{livekit.JobType_JT_ROOM},
		MaxJobs:      1,
		DrainTimeout: DefaultDrainTimeout,
		Logger:       core.GetLogger(),
		HTTPPort:     9999,
		Permissions: &livekit.ParticipantPermission{
			CanPublish:     true,
			CanSubscribe:   true,
			CanPublishData: true,
			Agent:          true,
		},
	}
}

type Provider struct {
	config Config
	logger *core.Logger

	// Connection
	state     atomic.Int32
	conn      *websocket.Conn
	connMu    sync.Mutex
	workerID  string
	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
	wg        sync.WaitGroup

	// Jobs
	activeJobs sync.Map // jobID -> *Job

	// Handlers
	jobHandler func(transport.ITransportService, context.Context) error

	// HTTP
	httpServer   *http.Server
	httpListener net.Listener
}

type Job struct {
	*livekit.Job
	Token     string
	URL       string
	Identity  string
	StartedAt time.Time
	Cancel    context.CancelFunc
	ctx       context.Context
}

func NewProvider(cfg Config) (*Provider, error) {
	if cfg.URL == "" || cfg.APIKey == "" || cfg.APISecret == "" {
		return nil, errors.New("URL, APIKey, and APISecret are required")
	}

	if cfg.DevMode {
		cfg.MaxJobs = 100 // effectively unlimited
		cfg.HTTPPort = 0  // random port
	}

	if cfg.Logger == nil {
		cfg.Logger = core.GetLogger()
	}

	ctx, cancel := context.WithCancel(context.Background())

	p := &Provider{
		config: cfg,
		logger: cfg.Logger.With(map[string]interface{}{"agent": cfg.AgentName}),
		ctx:    ctx,
		cancel: cancel,
	}
	p.state.Store(int32(StateDisconnected))

	return p, nil
}

func (p *Provider) RegisterJobHandler(handler func(transport.ITransportService, context.Context) error) error {
	if handler == nil {
		return errors.New("handler cannot be nil")
	}
	p.jobHandler = handler
	p.logger.Info("job handler registered")
	return nil
}

func (p *Provider) Start() error {
	p.logger.Info("starting provider: url=" + p.config.URL)

	if err := p.startHTTPServer(); err != nil {
		return err
	}

	p.wg.Add(1)
	go p.statusLoop()
	go p.connect()

	return nil
}

func (p *Provider) Stop() error {
	p.logger.Info("stopping provider")
	p.state.Store(int32(StateDraining))

	// Cancel context to signal all goroutines to stop
	p.cancel()

	// Wait for jobs to complete with timeout
	deadline := time.Now().Add(p.config.DrainTimeout)
	for time.Now().Before(deadline) {
		count := 0
		p.activeJobs.Range(func(_, _ interface{}) bool { count++; return true })
		if count == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Force cancel any remaining jobs
	p.activeJobs.Range(func(key, value interface{}) bool {
		if job, ok := value.(*Job); ok && job.Cancel != nil {
			job.Cancel()
		}
		return true
	})

	// Clean up connection
	p.cleanupConnection()

	// Stop HTTP server
	if p.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		p.httpServer.Shutdown(ctx)
	}

	// Close HTTP listener
	if p.httpListener != nil {
		p.httpListener.Close()
		p.httpListener = nil
	}

	// Wait for all goroutines to finish
	p.wg.Wait()

	return nil
}

func (p *Provider) cleanupConnection() {
	p.connMu.Lock()
	defer p.connMu.Unlock()

	if p.conn != nil {
		p.conn.Close()
		p.conn = nil
	}
}

func (p *Provider) connect() {
	// Don't reconnect if we're shutting down
	if p.ctx.Err() != nil {
		return
	}

	// Clean up any existing connection
	p.cleanupConnection()

	p.state.Store(int32(StateConnecting))

	u, _ := url.Parse(p.config.URL)
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	u.Path = "/agent"

	// Use AgentName as identity if set; otherwise fall back to a default.
	identity := p.config.AgentName
	if identity == "" {
		identity = "go-agent-worker"
	}
	token, err := auth.NewAccessToken(p.config.APIKey, p.config.APISecret).
		SetIdentity(identity).
		SetValidFor(24 * time.Hour).
		SetVideoGrant(&auth.VideoGrant{Agent: true}).
		ToJWT()
	if err != nil {
		p.logger.Error("failed to create token: " + err.Error())
		p.scheduleReconnect()
		return
	}

	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)

	conn, _, err := websocket.DefaultDialer.DialContext(p.ctx, u.String(), headers)
	if err != nil {
		p.logger.Error("failed to connect: " + err.Error())
		p.scheduleReconnect()
		return
	}

	p.connMu.Lock()
	p.conn = conn
	p.connMu.Unlock()

	// Register
	msg := &livekit.WorkerMessage{Message: &livekit.WorkerMessage_Register{
		Register: &livekit.RegisterWorkerRequest{
			Type:               p.config.JobTypes[0],
			AgentName:          p.config.AgentName,
			Version:            p.config.Version,
			AllowedPermissions: p.config.Permissions,
		},
	}}

	data, _ := proto.Marshal(msg)
	if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		p.logger.Error("failed to send register: " + err.Error())
		p.cleanupConnection()
		p.scheduleReconnect()
		return
	}

	p.state.Store(int32(StateConnected))
	p.wg.Add(1)
	go p.readLoop()
	p.wg.Add(1)
	go p.logStatsLoop()

	p.logger.Info("connected to LiveKit server")
}

func (p *Provider) scheduleReconnect() {
	// Don't reconnect if we're shutting down
	if p.ctx.Err() != nil {
		return
	}

	// Exponential backoff
	backoff := InitialReconnectDelay

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()

		select {
		case <-p.ctx.Done():
			return
		case <-time.After(backoff):
			p.connect()
		}
	}()
}

func (p *Provider) readLoop() {
	defer p.wg.Done()
	defer p.cleanupConnection()

	for {
		select {
		case <-p.ctx.Done():
			return
		default:
		}

		p.connMu.Lock()
		conn := p.conn
		p.connMu.Unlock()

		if conn == nil {
			return
		}

		_, data, err := conn.ReadMessage()
		if err != nil {
			if p.state.Load() != int32(StateDraining) {
				p.logger.Warn("read error: " + err.Error())
				p.state.Store(int32(StateDisconnected))
				p.scheduleReconnect()
			}
			return
		}

		var msg livekit.ServerMessage
		if err := proto.Unmarshal(data, &msg); err != nil {
			p.logger.Error("failed to unmarshal message: " + err.Error())
			continue
		}

		switch m := msg.Message.(type) {
		case *livekit.ServerMessage_Register:
			p.workerID = m.Register.WorkerId
			p.logger.Info("registered: worker_id=" + p.workerID)

		case *livekit.ServerMessage_Availability:
			p.handleJobRequest(m.Availability.Job)

		case *livekit.ServerMessage_Assignment:
			p.handleAssignment(m.Assignment)

		case *livekit.ServerMessage_Termination:
			if job, ok := p.activeJobs.Load(m.Termination.JobId); ok {
				job.(*Job).Cancel()
				p.activeJobs.Delete(m.Termination.JobId)
			}
		}
	}
}

func (p *Provider) handleJobRequest(job *livekit.Job) {
	if job == nil {
		return
	}

	// Check if already running in this room
	var exists bool
	p.activeJobs.Range(func(_, v interface{}) bool {
		if v.(*Job).Room.GetName() == job.Room.GetName() {
			exists = true
			return false
		}
		return true
	})

	available := !exists && p.state.Load() == int32(StateConnected)

	// Count active jobs
	count := 0
	p.activeJobs.Range(func(_, _ interface{}) bool { count++; return true })
	if uint32(count) >= p.config.MaxJobs {
		available = false
	}

	// Generate identity
	agentLabel := p.config.AgentName
	if agentLabel == "" {
		agentLabel = "agent"
	}
	identity := fmt.Sprintf("agent-%s-%s-%x",
		agentLabel,
		job.Id[len(job.Id)-8:],
		randBytes(4))

	// Send response
	resp := &livekit.AvailabilityResponse{
		JobId:               job.Id,
		Available:           available,
		ParticipantIdentity: identity,
		ParticipantName:     agentLabel,
	}

	msg := &livekit.WorkerMessage{Message: &livekit.WorkerMessage_Availability{
		Availability: resp,
	}}

	data, _ := proto.Marshal(msg)

	p.connMu.Lock()
	defer p.connMu.Unlock()
	if p.conn != nil {
		p.conn.WriteMessage(websocket.BinaryMessage, data)
	}
}

func (p *Provider) logStatsLoop() {
	defer p.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			count := 0
			p.activeJobs.Range(func(_, _ interface{}) bool { count++; return true })
			p.logger.Info(fmt.Sprintf("active jobs: %d", count))
		}
	}
}

func (p *Provider) handleAssignment(assign *livekit.JobAssignment) {
	job := assign.Job
	if job == nil || assign.Token == "" {
		return
	}

	ctx, cancel := context.WithCancel(p.ctx)

	j := &Job{
		Job:       job,
		Token:     assign.Token,
		URL:       p.config.URL,
		Identity:  assign.Job.State.ParticipantIdentity,
		StartedAt: time.Now(),
		Cancel:    cancel,
		ctx:       ctx,
	}

	p.activeJobs.Store(job.Id, j)

	p.wg.Add(1)
	go p.runJob(ctx, j)

	// Update status to running
	p.updateJobStatus(job.Id, livekit.JobStatus_JS_RUNNING, "")
}

func (p *Provider) runJob(ctx context.Context, job *Job) {
	defer p.wg.Done()
	defer func() {
		p.activeJobs.Delete(job.Id)
		p.updateJobStatus(job.Id, livekit.JobStatus_JS_SUCCESS, "")
	}()

	if p.jobHandler == nil {
		p.logger.Error("no job handler registered")
		p.updateJobStatus(job.Id, livekit.JobStatus_JS_FAILED, "no handler")
		return
	}

	// Create a per-session logger that tees to console + file.
	logDir := os.Getenv("LOG_DIR")
	if logDir == "" {
		logDir = "logs"
	}
	var sessionLogger *core.Logger
	logWriter, err := core.NewSessionLogWriter(logDir, job.Id, job.Room.GetName())
	if err != nil {
		p.logger.With(map[string]interface{}{"error": err}).Warn("failed to create session logger, using default")
		sessionLogger = p.logger
	} else {
		sessionLogger = core.NewSessionLogger(p.logger, logWriter)
	}
	if logWriter != nil {
		defer logWriter.Close()
	}

	jobLogger := sessionLogger.With(map[string]interface{}{"job": job.Id, "room": job.Room.GetName()})
	ctx = core.ContextWithSessionLogger(ctx, jobLogger)

	transport := NewLiveKitTransportWithToken(
		job.Room.GetName(),
		job.URL,
		job.Token,
		WithLogger(jobLogger),
	)

	if job.Participant != nil {
		transport.linkedParticipant = job.Participant.Identity
	}

	// Run the job handler in a goroutine and ALWAYS wait for it to finish.
	// This ensures Pipeline.Run() completes its runner.Stop() / Cleanup() cycle
	// before we proceed, preventing Pion goroutine and resource leaks.
	errChan := make(chan error, 1)
	go func() {
		errChan <- p.jobHandler(transport, ctx)
	}()

	// Wait for the handler to actually finish, regardless of context cancellation.
	// The handler (Pipeline.Run) listens to ctx internally and will clean up + return.
	if err := <-errChan; err != nil {
		p.updateJobStatus(job.Id, livekit.JobStatus_JS_FAILED, err.Error())
	}

	jobLogger.Info(fmt.Sprintf("job %s completed", job.Id))

	p.disconnectRoomParticipants(job.Room.GetName())
}

func (p *Provider) disconnectRoomParticipants(roomName string) {
	if p.config.APIKey == "" || p.config.APISecret == "" {
		p.logger.Warn("cannot disconnect participants: no API credentials", "room", roomName)
		return
	}

	svc := lksdk.NewRoomServiceClient(p.config.URL, p.config.APIKey, p.config.APISecret)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := svc.ListParticipants(ctx, &livekit.ListParticipantsRequest{Room: roomName})
	if err != nil {
		p.logger.Error("failed to list participants", "room", roomName, "error", err.Error())
		return
	}

	for _, participant := range resp.Participants {
		if participant.Kind == livekit.ParticipantInfo_AGENT {
			continue
		}
		if _, err := svc.RemoveParticipant(ctx, &livekit.RoomParticipantIdentity{
			Room:     roomName,
			Identity: participant.Identity,
		}); err != nil {
			p.logger.Error("failed to remove participant", "room", roomName, "participant", participant.Identity, "error", err.Error())
		} else {
			p.logger.Info("disconnected participant on agent done", "room", roomName, "participant", participant.Identity)
		}
	}
}

func (p *Provider) updateJobStatus(jobID string, status livekit.JobStatus, errMsg string) {
	msg := &livekit.WorkerMessage{Message: &livekit.WorkerMessage_UpdateJob{
		UpdateJob: &livekit.UpdateJobStatus{
			JobId:  jobID,
			Status: status,
			Error:  errMsg,
		},
	}}

	data, _ := proto.Marshal(msg)

	p.connMu.Lock()
	defer p.connMu.Unlock()
	if p.conn != nil {
		p.conn.WriteMessage(websocket.BinaryMessage, data)
	}
}

func (p *Provider) statusLoop() {
	defer p.wg.Done()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			if p.state.Load() != int32(StateConnected) {
				continue
			}

			count := 0
			p.activeJobs.Range(func(_, _ interface{}) bool { count++; return true })

			msg := &livekit.WorkerMessage{Message: &livekit.WorkerMessage_UpdateWorker{
				UpdateWorker: &livekit.UpdateWorkerStatus{
					Status:   livekit.WorkerStatus_WS_AVAILABLE.Enum(),
					JobCount: uint32(count),
				},
			}}

			data, _ := proto.Marshal(msg)

			p.connMu.Lock()
			if p.conn != nil {
				p.conn.WriteMessage(websocket.BinaryMessage, data)
			}
			p.connMu.Unlock()
		}
	}
}

func (p *Provider) startHTTPServer() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		state := State(p.state.Load())
		if state != StateConnected && state != StateDraining {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	addr := fmt.Sprintf("%s:%d", "", p.config.HTTPPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to start HTTP listener: %w", err)
	}

	p.httpListener = ln
	p.httpServer = &http.Server{Handler: mux}

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.httpServer.Serve(ln)
	}()

	return nil
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}
