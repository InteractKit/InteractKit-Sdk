package livekit

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"interactkit/core"
	"interactkit/events/llm"
	"interactkit/events/stt"
	"interactkit/events/tts"
	"interactkit/events/vad"
	"interactkit/events/vectordb"
	"interactkit/events/video"
	"interactkit/utils/audio"
	"log"
	"net"
	"sync"
	"time"

	media "github.com/livekit/media-sdk"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	lkmedia "github.com/livekit/server-sdk-go/v2/pkg/media"
	"github.com/pion/webrtc/v4"
)

// ConnectionState represents the connection state of the transport
type ConnectionState int

const (
	ConnectionStateDisconnected ConnectionState = iota
	ConnectionStateConnecting
	ConnectionStateConnected
	ConnectionStateReconnecting
)

func (s ConnectionState) String() string {
	switch s {
	case ConnectionStateDisconnected:
		return "disconnected"
	case ConnectionStateConnecting:
		return "connecting"
	case ConnectionStateConnected:
		return "connected"
	case ConnectionStateReconnecting:
		return "reconnecting"
	default:
		return "unknown"
	}
}

// DisconnectReason represents why a participant disconnected
type DisconnectReason int

const (
	DisconnectReasonUnknown DisconnectReason = iota
	DisconnectReasonClientInitiated
	DisconnectReasonRoomDeleted
	DisconnectReasonUserRejected
	DisconnectReasonServerShutdown
	DisconnectReasonDuplicateIdentity
)

func (r DisconnectReason) String() string {
	switch r {
	case DisconnectReasonUnknown:
		return "unknown"
	case DisconnectReasonClientInitiated:
		return "client_initiated"
	case DisconnectReasonRoomDeleted:
		return "room_deleted"
	case DisconnectReasonUserRejected:
		return "user_rejected"
	case DisconnectReasonServerShutdown:
		return "server_shutdown"
	case DisconnectReasonDuplicateIdentity:
		return "duplicate_identity"
	default:
		return "unknown"
	}
}

// ParticipantKind represents the type of participant
type ParticipantKind int

const (
	ParticipantKindStandard ParticipantKind = iota
	ParticipantKindConnector
	ParticipantKindSIP
	ParticipantKindAgent
)

// DefaultParticipantKinds are the participant kinds accepted by default
var DefaultParticipantKinds = []ParticipantKind{
	ParticipantKindStandard,
	ParticipantKindConnector,
	ParticipantKindSIP,
}

// CloseOnDisconnectReasons are disconnect reasons that trigger session close
var CloseOnDisconnectReasons = []DisconnectReason{
	DisconnectReasonClientInitiated,
	DisconnectReasonRoomDeleted,
	DisconnectReasonUserRejected,
}

// TextInputCallback is called when text input is received from a participant
type TextInputCallback func(transport *LiveKitTransport, text string, participant string)

// RoomInputOptions configures input handling
type RoomInputOptions struct {
	AudioSampleRate     int               // Sample rate for audio input (default: 24000)
	AudioNumChannels    int               // Number of audio channels (default: 1)
	TextEnabled         bool              // Enable text input (default: true)
	AudioEnabled        bool              // Enable audio input (default: true)
	VideoEnabled        bool              // Enable video input (default: false)
	ParticipantIdentity string            // Specific participant to link to (optional)
	ParticipantKinds    []ParticipantKind // Accepted participant kinds
	CloseOnDisconnect   bool              // Close session on participant disconnect (default: true)
	TextInputCallback   TextInputCallback // Callback for text input
}

// RoomOutputOptions configures output handling
type RoomOutputOptions struct {
	TranscriptionEnabled bool   // Enable transcription output (default: true)
	AudioEnabled         bool   // Enable audio output (default: true)
	AudioSampleRate      int    // Sample rate for audio output (default: 24000)
	AudioNumChannels     int    // Number of audio channels (default: 1)
	SyncTranscription    bool   // Sync transcription with audio (default: true)
	AudioTrackName       string // Name of the audio track (default: "agent_audio")
	AgentName            string // Name of the agent (default: "agent")
}

// DefaultRoomInputOptions returns default input options
func DefaultRoomInputOptions() RoomInputOptions {
	return RoomInputOptions{
		AudioSampleRate:   24000,
		AudioNumChannels:  1,
		TextEnabled:       true,
		AudioEnabled:      true,
		VideoEnabled:      false,
		ParticipantKinds:  DefaultParticipantKinds,
		CloseOnDisconnect: true,
	}
}

// DefaultRoomOutputOptions returns default output options
func DefaultRoomOutputOptions() RoomOutputOptions {
	return RoomOutputOptions{
		TranscriptionEnabled: true,
		AudioEnabled:         true,
		AudioSampleRate:      24000,
		AudioNumChannels:     1,
		SyncTranscription:    true,
		AudioTrackName:       "agent_audio",
		AgentName:            "agent",
	}
}

// TranscriptionEvent represents a transcription update
type TranscriptionEvent struct {
	Text        string
	Participant string
	IsFinal     bool
	TrackID     string
}

// LiveKitTransport implements core.TransportService with enhanced features
type LiveKitTransport struct {
	ctx       context.Context
	cancel    context.CancelFunc
	room      string
	url       string
	apiKey    string
	apiSecret string
	token     string // For token-based authentication (worker mode)
	useToken  bool   // Whether to use token-based auth
	client    *lksdk.Room

	// Connection state
	connectionState ConnectionState
	connected       bool
	closed          bool
	closeOnce       sync.Once
	wg              sync.WaitGroup

	// Options
	inputOptions  RoomInputOptions
	outputOptions RoomOutputOptions

	// Tracks
	audioTrack *lkmedia.PCMLocalTrack
	videoTrack *lksdk.LocalTrack

	// Track management
	activeTracks      sync.Map // map[string]context.CancelFunc
	participantTracks sync.Map // map[string][]string // participant -> track IDs

	// Participant management
	linkedParticipant       string
	participantAvailableCh  chan *lksdk.RemoteParticipant
	participantDisconnectCh chan DisconnectReason

	// Transcription
	transcriptionChan chan<- TranscriptionEvent

	// Synchronization
	mu          sync.RWMutex
	recvRunning bool

	// Channels - Using core.MediaChunk to match interface
	outputChan chan<- core.MediaChunk
	errorChan  chan<- error

	// State change callbacks
	onConnectionStateChanged  func(ConnectionState)
	onParticipantConnected    func(string)
	onParticipantDisconnected func(string, DisconnectReason)

	logger *core.Logger
}

// LiveKitOption is a functional option for configuring LiveKitTransport
type LiveKitOption func(*LiveKitTransport)

// WithInputOptions sets the input options
func WithInputOptions(opts RoomInputOptions) LiveKitOption {
	return func(l *LiveKitTransport) {
		l.inputOptions = opts
	}
}

// WithOutputOptions sets the output options
func WithOutputOptions(opts RoomOutputOptions) LiveKitOption {
	return func(l *LiveKitTransport) {
		l.outputOptions = opts
	}
}

// WithLogger sets a custom logger
func WithLogger(logger *core.Logger) LiveKitOption {
	return func(l *LiveKitTransport) {
		l.logger = logger
	}
}

// WithParticipantIdentity sets the participant to link to
func WithParticipantIdentity(identity string) LiveKitOption {
	return func(l *LiveKitTransport) {
		l.linkedParticipant = identity
		l.inputOptions.ParticipantIdentity = identity
	}
}

// WithConnectionStateCallback sets the connection state change callback
func WithConnectionStateCallback(cb func(ConnectionState)) LiveKitOption {
	return func(l *LiveKitTransport) {
		l.onConnectionStateChanged = cb
	}
}

// WithParticipantCallbacks sets participant event callbacks
func WithParticipantCallbacks(onConnect func(string), onDisconnect func(string, DisconnectReason)) LiveKitOption {
	return func(l *LiveKitTransport) {
		l.onParticipantConnected = onConnect
		l.onParticipantDisconnected = onDisconnect
	}
}

// WithToken sets the authentication token (for worker mode)
func WithToken(token string) LiveKitOption {
	return func(l *LiveKitTransport) {
		l.token = token
		l.useToken = true
	}
}

// NewLiveKitTransport creates a new transport with API key authentication
func NewLiveKitTransport(
	room, url, apiKey, apiSecret string,
	opts ...LiveKitOption,
) *LiveKitTransport {
	t := &LiveKitTransport{
		room:                    room,
		url:                     url,
		apiKey:                  apiKey,
		apiSecret:               apiSecret,
		inputOptions:            DefaultRoomInputOptions(),
		outputOptions:           DefaultRoomOutputOptions(),
		connectionState:         ConnectionStateDisconnected,
		participantAvailableCh:  make(chan *lksdk.RemoteParticipant, 1),
		participantDisconnectCh: make(chan DisconnectReason, 1),
		logger:                  core.GetLogger(),
		closed:                  false,
	}

	for _, opt := range opts {
		opt(t)
	}

	return t
}

// NewLiveKitTransportWithToken creates a new transport with token authentication
// This is used in worker mode when the token is provided by job assignment
func NewLiveKitTransportWithToken(
	roomName string,
	url string,
	token string,
	opts ...LiveKitOption,
) *LiveKitTransport {

	// Add token option to the list
	tokenOpt := WithToken(token)
	allOpts := append([]LiveKitOption{tokenOpt}, opts...)

	// Create transport without API credentials
	return NewLiveKitTransport(
		roomName,
		url,
		"", // API key not needed when using token
		"", // API secret not needed when using token
		allOpts...,
	)
}

// Connect establishes connection to LiveKit room
func (l *LiveKitTransport) Connect() error {
	l.mu.RLock()
	closed := l.closed
	l.mu.RUnlock()
	if closed {
		return errors.New("transport is closed")
	}

	if l.useToken && l.token != "" {
		return l.connectWithToken()
	}
	return l.connectWithAPIKey()
}

// Add these methods to LiveKitTransport
func (l *LiveKitTransport) isConnected() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.connected
}

func (l *LiveKitTransport) setConnected(connected bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.connected = connected
}

// connectWithToken uses token-based authentication (worker mode)
func (l *LiveKitTransport) connectWithToken() error {
	if l.room == "" {
		return errors.New("room name cannot be empty")
	}
	if l.token == "" {
		return errors.New("token cannot be empty")
	}

	l.setConnectionState(ConnectionStateConnecting)

	roomCB := l.createRoomCallback()

	// Enable auto-subscribe to ensure we get all tracks
	roomOpts := []lksdk.ConnectOption{
		lksdk.WithAutoSubscribe(true), // This is key - auto-subscribe to all tracks
	}

	l.logger.Info("connecting to room with token",
		"room", l.room,
		"url", l.url,
		"autoSubscribe", true,
	)

	room, err := lksdk.ConnectToRoomWithToken(
		l.url,
		l.token,
		roomCB,
		roomOpts...,
	)
	if err != nil {
		l.setConnectionState(ConnectionStateDisconnected)
		return fmt.Errorf("failed to connect to room with token: %w", err)
	}

	return l.onRoomConnected(room)
}

// connectWithAPIKey uses API key authentication (direct mode)
func (l *LiveKitTransport) connectWithAPIKey() error {
	if l.room == "" {
		return errors.New("room name cannot be empty")
	}
	if l.apiKey == "" || l.apiSecret == "" {
		return errors.New("API key and secret are required for direct connection")
	}

	l.setConnectionState(ConnectionStateConnecting)

	// Generate unique identity for this agent instance
	agentName := l.outputOptions.AgentName
	if agentName == "" {
		agentName = "agent"
	}
	identity := fmt.Sprintf("agent-%s-%s", agentName, randomString(8))

	roomCB := l.createRoomCallback()

	// Enable auto-subscribe to ensure we get all tracks
	roomOpts := []lksdk.ConnectOption{
		lksdk.WithAutoSubscribe(true), // This is key - auto-subscribe to all tracks
	}

	l.logger.Info("connecting to room with API key",
		"room", l.room,
		"url", l.url,
		"identity", identity,
		"agentName", agentName,
		"autoSubscribe", true,
	)

	room, err := lksdk.ConnectToRoom(
		l.url,
		lksdk.ConnectInfo{
			APIKey:              l.apiKey,
			APISecret:           l.apiSecret,
			RoomName:            l.room,
			ParticipantIdentity: identity,
			ParticipantName:     agentName,
			ParticipantKind:     lksdk.ParticipantAgent,
			ParticipantMetadata: fmt.Sprintf(`{"agent":true,"name":"%s"}`, agentName),
		},
		roomCB,
		roomOpts...,
	)
	if err != nil {
		l.setConnectionState(ConnectionStateDisconnected)
		return fmt.Errorf("failed to connect to room with API key: %w", err)
	}

	return l.onRoomConnected(room)
}

// createRoomCallback creates the room callback handler
func (l *LiveKitTransport) createRoomCallback() *lksdk.RoomCallback {
	return &lksdk.RoomCallback{
		ParticipantCallback: lksdk.ParticipantCallback{
			OnTrackSubscribed: func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
				l.handleRemoteTrack(track, pub, rp)
			},
			OnTrackUnsubscribed: func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
				l.handleTrackUnsubscribed(track, pub, rp)
			},
			OnDataPacket: func(data lksdk.DataPacket, params lksdk.DataReceiveParams) {
				l.handleDataPacket(data, params)
			},
		},
		OnParticipantConnected: func(rp *lksdk.RemoteParticipant) {
			l.handleParticipantConnected(rp)
			// Explicitly subscribe to tracks of newly connected participants
			// (though auto-subscribe should handle this, this is a fallback)
			l.subscribeToParticipantTracks(rp)
		},
		OnParticipantDisconnected: func(rp *lksdk.RemoteParticipant) {
			l.handleParticipantDisconnected(rp)
		},
		OnReconnecting: func() {
			l.setConnectionState(ConnectionStateReconnecting)
			l.logger.Info("reconnecting to room")
		},
		OnReconnected: func() {
			l.setConnectionState(ConnectionStateConnected)
			l.logger.Info("reconnected to room")
		},
		OnDisconnected: func() {
			l.setConnectionState(ConnectionStateDisconnected)
			l.logger.Info("disconnected from room")
		},
	}
}

// handleTrackUnsubscribed cleans up resources when a track is unsubscribed
func (l *LiveKitTransport) handleTrackUnsubscribed(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	trackID := track.ID()
	participantID := rp.Identity()

	l.logger.Info("track unsubscribed", "trackID", trackID, "participant", participantID)

	// Cancel the track reading goroutine
	if cancel, ok := l.activeTracks.Load(trackID); ok {
		cancel.(context.CancelFunc)()
		l.activeTracks.Delete(trackID)
	}

	// Remove from participant tracks map
	if trackIDs, ok := l.participantTracks.Load(participantID); ok {
		trackList := trackIDs.([]string)
		newList := make([]string, 0, len(trackList))
		for _, id := range trackList {
			if id != trackID {
				newList = append(newList, id)
			}
		}
		if len(newList) == 0 {
			l.participantTracks.Delete(participantID)
		} else {
			l.participantTracks.Store(participantID, newList)
		}
	}
}

// subscribeToParticipantTracks explicitly subscribes to all tracks from a participant
func (l *LiveKitTransport) subscribeToParticipantTracks(rp *lksdk.RemoteParticipant) {
	// Check if this participant is one we care about
	l.mu.RLock()
	linkedParticipant := l.linkedParticipant
	targetParticipant := l.inputOptions.ParticipantIdentity
	audioEnabled := l.inputOptions.AudioEnabled
	videoEnabled := l.inputOptions.VideoEnabled
	l.mu.RUnlock()

	// Skip if not our target participant
	if targetParticipant != "" && rp.Identity() != targetParticipant {
		return
	}
	if linkedParticipant != "" && rp.Identity() != linkedParticipant {
		return
	}

	// Subscribe to audio tracks
	if audioEnabled {
		for _, pub := range rp.TrackPublications() {
			// Check if it's an audio track we want
			isMicrophone := pub.Source() == livekit.TrackSource_MICROPHONE
			isScreenShareAudio := pub.Source() == livekit.TrackSource_SCREEN_SHARE_AUDIO

			if (isMicrophone || isScreenShareAudio) && pub.Kind() == lksdk.TrackKindAudio {
				// Cast to RemoteTrackPublication to access SetSubscribed
				if remotePub, ok := pub.(*lksdk.RemoteTrackPublication); ok {
					if !remotePub.IsSubscribed() {
						l.logger.Info("subscribing to audio track",
							"participant", rp.Identity(),
							"trackID", remotePub.SID(),
							"source", remotePub.Source())

						err := remotePub.SetSubscribed(true)
						if err != nil {
							l.logger.Error("failed to subscribe to audio track",
								"error", err,
								"participant", rp.Identity(),
								"trackID", remotePub.SID())
						}
					}
				}
			}
		}
	}

	// Subscribe to video tracks
	if videoEnabled {
		for _, pub := range rp.TrackPublications() {
			// Check if it's a video track we want
			isCamera := pub.Source() == livekit.TrackSource_CAMERA
			isScreenShare := pub.Source() == livekit.TrackSource_SCREEN_SHARE

			if (isCamera || isScreenShare) && pub.Kind() == lksdk.TrackKindVideo {
				if remotePub, ok := pub.(*lksdk.RemoteTrackPublication); ok {
					if !remotePub.IsSubscribed() {
						l.logger.Info("subscribing to video track",
							"participant", rp.Identity(),
							"trackID", remotePub.SID(),
							"source", remotePub.Source())

						err := remotePub.SetSubscribed(true)
						if err == nil {
							receiver := remotePub.Receiver()
							if receiver != nil {
								track := receiver.Track()
								if track != nil {
									l.logger.Info("successfully subscribed to video track",
										"participant", rp.Identity(),
										"trackID", remotePub.SID(),
										"source", remotePub.Source())
								}
							}
						}
						if err != nil {
							l.logger.Error("failed to subscribe to video track",
								"error", err,
								"participant", rp.Identity(),
								"trackID", remotePub.SID())
						}
					}
				}
			}
		}
	}
}

// onRoomConnected handles successful room connection
func (l *LiveKitTransport) onRoomConnected(room *lksdk.Room) error {
	log.Printf("connected to LiveKit room: %s\n", room.Name())
	l.client = room
	l.setConnected(true)
	l.setConnectionState(ConnectionStateConnected)

	// Set agent attributes first - this identifies us as an agent to LiveKit
	if err := l.setAgentAttributes(); err != nil {
		l.logger.Warn("failed to set agent attributes", "error", err)
	}

	// Create and publish audio track if enabled
	if l.outputOptions.AudioEnabled {
		if err := l.createAudioTrack(); err != nil {
			l.logger.Warn("failed to create audio track", "error", err)
			return err
		}
	}

	// Process existing participants and subscribe to their tracks
	for _, p := range room.GetRemoteParticipants() {
		l.handleParticipantConnected(p)
		// Explicitly subscribe to tracks of existing participants
		l.subscribeToParticipantTracks(p)

		// Check if this is the participant we're waiting for
		l.mu.RLock()
		linked := l.linkedParticipant
		target := l.inputOptions.ParticipantIdentity
		l.mu.RUnlock()

		// If we're waiting for a specific participant and this is them,
		// or if we accept any participant and haven't linked yet
		if (target != "" && p.Identity() == target) ||
			(target == "" && linked == "") {
			select {
			case l.participantAvailableCh <- p:
				l.logger.Debug("signaled participant availability",
					"participant", p.Identity())
			default:
				// Channel already has a value, which is fine
				l.logger.Debug("participant available channel already has value",
					"participant", p.Identity())
			}
		}
	}

	// Log connection info
	identity := "unknown"
	if room.LocalParticipant != nil {
		identity = room.LocalParticipant.Identity()
	}

	l.logger.Info("connected to LiveKit room",
		"room", l.room,
		"identity", identity,
		"url", l.url,
	)

	return nil
}

// handleRemoteTrack processes incoming remote audio/video tracks
func (l *LiveKitTransport) handleRemoteTrack(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	// Check if audio/video is enabled
	isAudio := track.Kind() == webrtc.RTPCodecTypeAudio
	isVideo := track.Kind() == webrtc.RTPCodecTypeVideo

	if isAudio && !l.inputOptions.AudioEnabled {
		l.logger.Debug("ignoring audio track (disabled)", "trackID", track.ID())
		return
	}
	if isVideo && !l.inputOptions.VideoEnabled {
		l.logger.Debug("ignoring video track (disabled)", "trackID", track.ID())
		return
	}

	// Check if from linked participant
	l.mu.RLock()
	linkedParticipant := l.linkedParticipant
	l.mu.RUnlock()

	if linkedParticipant != "" && rp.Identity() != linkedParticipant {
		l.logger.Debug("ignoring track from non-linked participant",
			"trackID", track.ID(),
			"participant", rp.Identity(),
			"linked", linkedParticipant,
		)
		return
	}

	// Infer audio parameters from track
	var sampleRate, numChannels int
	if isAudio {
		codec := track.Codec()

		// Try to get sample rate from codec parameters
		switch {
		case codec.MimeType == "audio/opus":
			// Opus typically uses 48kHz but can be negotiated
			// Default to 48kHz for Opus
			sampleRate = 48000
			numChannels = 1

		case codec.MimeType == "audio/PCMU" || codec.MimeType == "audio/PCMA":
			// G.711 codecs are always 8kHz, mono
			sampleRate = 8000
			numChannels = 1

		case codec.MimeType == "audio/G722":
			// G.722 is 16kHz
			sampleRate = 16000
			numChannels = 1

		default:
			// Fallback to configured values
			sampleRate = l.inputOptions.AudioSampleRate
			numChannels = l.inputOptions.AudioNumChannels
		}

		l.logger.Info("inferred audio parameters from track",
			"trackID", track.ID(),
			"mimeType", codec.MimeType,
			"clockRate", codec.ClockRate,
			"channels", codec.Channels,
			"sampleRate", sampleRate,
			"numChannels", numChannels,
			"participant", rp.Identity(),
		)
	}

	l.logger.Info("subscribed to track",
		"trackID", track.ID(),
		"kind", track.Kind().String(),
		"codec", track.Codec().MimeType,
		"participant", rp.Identity(),
		"room", l.room,
	)

	// Start reading the track with inferred parameters - add to WaitGroup
	l.wg.Add(1)
	go l.readTrack(track, rp, sampleRate, numChannels)
}

// readTrack continuously reads RTP packets from a track and converts to MediaChunk
func (l *LiveKitTransport) readTrack(track *webrtc.TrackRemote, rp *lksdk.RemoteParticipant, sampleRate, numChannels int) {
	defer l.wg.Done()

	trackID := track.ID()
	participantID := rp.Identity()

	// Create cancellable context for this track
	ctx, cancel := context.WithCancel(l.ctx)

	// Store cancellation function
	l.activeTracks.Store(trackID, cancel)

	// Track which participant owns this track
	var trackList []string
	if val, ok := l.participantTracks.Load(participantID); ok {
		trackList = val.([]string)
	}
	trackList = append(trackList, trackID)
	l.participantTracks.Store(participantID, trackList)

	l.logger.Info("started reading track",
		"trackID", trackID,
		"participant", participantID,
		"kind", track.Kind().String(),
	)

	defer func() {
		// Clean up on exit
		l.activeTracks.Delete(trackID)
		cancel()

		// Remove from participant tracks map
		if trackIDs, ok := l.participantTracks.Load(participantID); ok {
			trackList := trackIDs.([]string)
			newList := make([]string, 0, len(trackList))
			for _, id := range trackList {
				if id != trackID {
					newList = append(newList, id)
				}
			}
			if len(newList) == 0 {
				l.participantTracks.Delete(participantID)
			} else {
				l.participantTracks.Store(participantID, newList)
			}
		}

		l.logger.Info("stopped reading track",
			"trackID", trackID,
			"participant", participantID)
	}()

	// Recover from panics to prevent crashing the transport
	defer func() {
		if r := recover(); r != nil {
			l.logger.Error("panic reading track",
				"recover", r,
				"trackID", trackID,
				"participant", participantID)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			l.logger.Debug("context done, stopping track read",
				"trackID", trackID,
				"participant", participantID)
			return
		case <-l.ctx.Done():
			return
		default:
			// Set read deadline to prevent blocking forever
			if err := track.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				// Track might be closed, check if we should exit
				select {
				case <-ctx.Done():
					return
				case <-l.ctx.Done():
					return
				default:
					continue
				}
			}

			// Use ReadRTP to properly parse RTP packets and extract the payload
			rtpPacket, _, err := track.ReadRTP()
			if err != nil {
				// Check if error is due to timeout or closed track
				if err.Error() == "EOF" || err.Error() == "track closed" {
					l.logger.Debug("track closed normally",
						"trackID", trackID,
						"participant", participantID)
					return
				}

				// For timeout, just continue
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}

				// For other errors, check if we should exit
				select {
				case <-ctx.Done():
					return
				case <-l.ctx.Done():
					return
				default:
					l.mu.RLock()
					errChan := l.errorChan
					l.mu.RUnlock()

					if errChan != nil {
						select {
						case errChan <- fmt.Errorf("track read error for %s: %w", trackID, err):
						case <-ctx.Done():
						case <-l.ctx.Done():
						default:
						}
					}
					l.logger.Error("track read error",
						"error", err,
						"trackID", trackID,
						"participant", participantID)
					continue
				}
			}

			if rtpPacket == nil || len(rtpPacket.Payload) == 0 {
				continue
			}

			// Get the actual audio/video payload from the RTP packet (without RTP headers)
			payload := make([]byte, len(rtpPacket.Payload))
			copy(payload, rtpPacket.Payload)

			// Create MediaChunk based on track type
			chunk := core.MediaChunk{}

			if track.Kind() == webrtc.RTPCodecTypeAudio {
				// Use inferred parameters for audio
				chunk.Audio = core.AudioChunk{
					Data:       &payload,
					SampleRate: sampleRate,
					Channels:   numChannels,
					Format:     core.OPUS, // Audio from LiveKit is typically OPUS
					Timestamp:  time.Now(),
				}
			} else if track.Kind() == webrtc.RTPCodecTypeVideo {
				chunk.Video = core.VideoChunk{
					Data:      &payload,
					Format:    core.VideoFormatMP4,
					Timestamp: time.Now(),
				}
			}

			// Get current output channel
			l.mu.RLock()
			outputChan := l.outputChan
			l.mu.RUnlock()

			// Try to send chunk
			if outputChan != nil {
				select {
				case outputChan <- chunk:
				case <-ctx.Done():
					return
				case <-l.ctx.Done():
					return
				default:
					l.logger.Debug("output channel is full, dropping chunk",
						"trackID", trackID,
						"participant", participantID)
				}
			}
		}
	}
}

// handleParticipantConnected processes a newly connected participant
func (l *LiveKitTransport) handleParticipantConnected(rp *lksdk.RemoteParticipant) {
	identity := rp.Identity()

	// Skip if it's ourselves
	if l.client != nil && l.client.LocalParticipant != nil {
		if identity == l.client.LocalParticipant.Identity() {
			return
		}
	}

	// Check if we're looking for a specific participant
	if l.inputOptions.ParticipantIdentity != "" && identity != l.inputOptions.ParticipantIdentity {
		return
	}

	// Check participant kind filtering
	if !l.isParticipantKindAccepted(rp) {
		return
	}

	l.mu.Lock()
	wasEmpty := l.linkedParticipant == ""
	if wasEmpty {
		l.linkedParticipant = identity
	}
	l.mu.Unlock()

	// Notify via channel (non-blocking) only if we just linked to this participant
	if wasEmpty {
		select {
		case l.participantAvailableCh <- rp:
			l.logger.Debug("signaled participant availability",
				"participant", identity)
		default:
			l.logger.Debug("participant available channel already has value",
				"participant", identity)
		}
	}

	// Call callback if set
	if l.onParticipantConnected != nil {
		l.onParticipantConnected(identity)
	}

	l.logger.Info("participant connected",
		"identity", identity,
		"room", l.room,
	)
}

// SetParticipant switches to a different participant
func (l *LiveKitTransport) SetParticipant(identity string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if identity == "" {
		l.linkedParticipant = ""
		l.logger.Debug("unset linked participant")
		return
	}

	l.linkedParticipant = identity
	l.logger.Debug("set linked participant", "identity", identity)

	// Check if participant is already connected and signal if so
	if l.client != nil {
		for _, p := range l.client.GetRemoteParticipants() {
			if p.Identity() == identity {
				select {
				case l.participantAvailableCh <- p:
					l.logger.Debug("signaled existing participant availability",
						"participant", identity)
				default:
					l.logger.Debug("participant available channel already has value",
						"participant", identity)
				}
				break
			}
		}
	}
}

// WaitForParticipant waits for a participant to be available
func (l *LiveKitTransport) WaitForParticipant(ctx context.Context) (*lksdk.RemoteParticipant, error) {
	// First, check if we already have a linked participant
	l.mu.RLock()
	linked := l.linkedParticipant
	l.mu.RUnlock()

	if linked != "" && l.client != nil {
		// Check if that participant is already in the room
		for _, p := range l.client.GetRemoteParticipants() {
			if p.Identity() == linked {
				l.logger.Debug("participant already available", "participant", linked)
				return p, nil
			}
		}
	}

	// If not, wait for them to become available
	select {
	case p := <-l.participantAvailableCh:
		return p, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.ctx.Done():
		return nil, l.ctx.Err()
	}
}

// setAgentAttributes sets all required agent attributes on the local participant
func (l *LiveKitTransport) setAgentAttributes() error {
	if l.client == nil || l.client.LocalParticipant == nil {
		return errors.New("not connected")
	}

	agentName := l.outputOptions.AgentName
	if agentName == "" {
		agentName = "agent"
	}

	l.client.LocalParticipant.SetAttributes(map[string]string{
		"lk.agent.state": "listening",
		"lk.agent.name":  agentName,
	})

	l.logger.Debug("set agent attributes",
		"name", agentName,
		"state", "listening",
	)

	return nil
}

// createAudioTrack creates and publishes an audio track
func (l *LiveKitTransport) createAudioTrack() error {
	audioTrack, err := lkmedia.NewPCMLocalTrack(
		l.outputOptions.AudioSampleRate,
		l.outputOptions.AudioNumChannels,
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to create audio track: %w", err)
	}
	l.audioTrack = audioTrack

	pub, err := l.client.LocalParticipant.PublishTrack(audioTrack, &lksdk.TrackPublicationOptions{
		Name:   l.outputOptions.AudioTrackName,
		Source: livekit.TrackSource_MICROPHONE,
	})
	if err != nil {
		return fmt.Errorf("failed to publish audio track: %w", err)
	}

	l.logger.Info("audio track created and published",
		"name", l.outputOptions.AudioTrackName,
		"sampleRate", l.outputOptions.AudioSampleRate,
		"channels", l.outputOptions.AudioNumChannels,
		"trackSID", pub.SID(),
	)

	return nil
}

// Initialize is kept for backward compatibility
func (l *LiveKitTransport) Initialize(ctx context.Context) error {
	l.ctx, l.cancel = context.WithCancel(ctx)
	return nil
}

// setConnectionState updates the connection state and notifies callback
func (l *LiveKitTransport) setConnectionState(state ConnectionState) {
	l.mu.Lock()
	oldState := l.connectionState
	l.connectionState = state
	cb := l.onConnectionStateChanged
	l.mu.Unlock()

	if oldState != state {
		l.logger.Debug("connection state changed",
			"from", oldState.String(),
			"to", state.String(),
		)
		if cb != nil {
			cb(state)
		}
	}
}

// GetConnectionState returns the current connection state
func (l *LiveKitTransport) GetConnectionState() ConnectionState {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.connectionState
}

// setAgentState sets the agent state attribute on the local participant
func (l *LiveKitTransport) setAgentState(state string) error {
	if l.client == nil || l.client.LocalParticipant == nil {
		return errors.New("not connected")
	}
	l.client.LocalParticipant.SetAttributes(map[string]string{
		"lk.agent.state": state,
	})
	return nil
}

// SetAgentState updates the agent state (listening, thinking, speaking)
func (l *LiveKitTransport) SetAgentState(state string) error {
	return l.setAgentState(state)
}

// handleParticipantDisconnected processes a disconnected participant
func (l *LiveKitTransport) handleParticipantDisconnected(rp *lksdk.RemoteParticipant) {
	identity := rp.Identity()

	// Skip if it's ourselves
	if l.client != nil && l.client.LocalParticipant != nil {
		if identity == l.client.LocalParticipant.Identity() {
			return
		}
	}

	// Clean up all tracks for this participant
	if trackIDs, ok := l.participantTracks.Load(identity); ok {
		for _, trackID := range trackIDs.([]string) {
			if cancel, ok := l.activeTracks.Load(trackID); ok {
				cancel.(context.CancelFunc)() // Stop the track reading goroutine
			}
			l.activeTracks.Delete(trackID)
		}
		l.participantTracks.Delete(identity)
	}

	l.mu.Lock()
	wasLinked := l.linkedParticipant == identity
	if wasLinked {
		l.linkedParticipant = ""
	}
	l.mu.Unlock()

	reason := l.mapDisconnectReason(rp)

	// Call callback if set
	if l.onParticipantDisconnected != nil {
		l.onParticipantDisconnected(identity, reason)
	}

	// Check if we should close on disconnect
	if wasLinked && l.inputOptions.CloseOnDisconnect && l.shouldCloseOnDisconnect(reason) {
		l.logger.Info("closing due to participant disconnect",
			"participant", identity,
			"reason", reason.String(),
			"room", l.room,
		)
		select {
		case l.participantDisconnectCh <- reason:
		default:
		}
	}

	l.logger.Info("participant disconnected",
		"identity", identity,
		"reason", reason.String(),
		"room", l.room,
	)
}

// mapDisconnectReason maps LiveKit disconnect reason to our enum
func (l *LiveKitTransport) mapDisconnectReason(rp *lksdk.RemoteParticipant) DisconnectReason {
	// Note: The actual reason would come from the participant info
	// This is a simplified mapping
	return DisconnectReasonClientInitiated
}

// shouldCloseOnDisconnect checks if the disconnect reason should trigger close
func (l *LiveKitTransport) shouldCloseOnDisconnect(reason DisconnectReason) bool {
	for _, r := range CloseOnDisconnectReasons {
		if r == reason {
			return true
		}
	}
	return false
}

// isParticipantKindAccepted checks if a participant kind is in the accepted list
func (l *LiveKitTransport) isParticipantKindAccepted(rp *lksdk.RemoteParticipant) bool {
	// Map LiveKit participant kind to our enum
	kind := l.mapParticipantKind(rp)
	for _, k := range l.inputOptions.ParticipantKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// mapParticipantKind maps LiveKit participant kind to our enum
func (l *LiveKitTransport) mapParticipantKind(rp *lksdk.RemoteParticipant) ParticipantKind {
	// Check attributes for kind
	if rp.Attributes() != nil {
		if kind, ok := rp.Attributes()["lk.participant.kind"]; ok {
			switch kind {
			case "standard":
				return ParticipantKindStandard
			case "connector":
				return ParticipantKindConnector
			case "sip":
				return ParticipantKindSIP
			case "agent":
				return ParticipantKindAgent
			}
		}
	}
	return ParticipantKindStandard
}

// handleDataPacket processes incoming data packets and converts to MediaChunk
func (l *LiveKitTransport) handleDataPacket(data lksdk.DataPacket, params lksdk.DataReceiveParams) {
	if !l.inputOptions.TextEnabled {
		return
	}

	// Check if from linked participant
	l.mu.RLock()
	linkedParticipant := l.linkedParticipant
	l.mu.RUnlock()

	if linkedParticipant != "" && params.SenderIdentity != linkedParticipant {
		return
	}

	if l.outputChan == nil {
		return
	}

	payload := data.ToProto().GetUser().GetPayload()

	// Skip empty messages
	if len(payload) == 0 {
		return
	}

	// Create MediaChunk with text
	chunk := core.MediaChunk{
		Text: core.TextChunk{
			Text: string(payload),
		},
	}

	// Call text input callback if set
	if l.inputOptions.TextInputCallback != nil {
		go l.inputOptions.TextInputCallback(l, string(payload), params.SenderIdentity)
	}

	select {
	case l.outputChan <- chunk:
		l.logger.Debug("sent text to pipeline",
			"participant", params.SenderIdentity,
			"text", string(payload),
		)
	case <-l.ctx.Done():
		return
	}
}

// Cleanup disconnects and cancels context
func (l *LiveKitTransport) Cleanup() error {
	var cleanupErr error

	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.closed = true
		l.mu.Unlock()

		l.logger.Info("Cleanup: starting", "room", l.room)

		// 1. Cancel context first to signal all goroutines to stop
		l.logger.Info("Cleanup: cancelling context", "room", l.room)
		if l.cancel != nil {
			l.cancel()
		}

		// 2. Cancel all active track reading goroutines explicitly
		l.activeTracks.Range(func(key, value interface{}) bool {
			if cancel, ok := value.(context.CancelFunc); ok {
				cancel()
			}
			l.activeTracks.Delete(key)
			return true
		})

		// 3. Close audio/video tracks BEFORE disconnecting the client
		//    so Pion's PeerConnection can cleanly unpublish them
		l.mu.Lock()
		if l.audioTrack != nil {
			l.logger.Info("Cleanup: closing audio track", "room", l.room)
			l.audioTrack.Close()
			l.audioTrack = nil
		}
		if l.videoTrack != nil {
			l.logger.Info("Cleanup: closing video track", "room", l.room)
			l.videoTrack.Close()
			l.videoTrack = nil
		}

		// Clear channels to unblock any goroutines trying to send
		l.logger.Info("Cleanup: clearing output, error, and transcription channels", "room", l.room)
		l.outputChan = nil
		l.errorChan = nil
		l.transcriptionChan = nil
		l.mu.Unlock()

		// 4. Wait for all goroutines (track readers, receive loop) to finish
		//    BEFORE disconnecting, so they exit cleanly via context cancellation
		l.logger.Info("Cleanup: waiting for goroutines to finish", "room", l.room)
		l.wg.Wait()
		l.logger.Info("Cleanup: all goroutines finished", "room", l.room)

		// 5. Now disconnect the LiveKit client (closes Pion PeerConnection)
		if l.client != nil {
			l.logger.Info("Cleanup: disconnecting LiveKit client", "room", l.room)
			l.client.Disconnect()
			l.client = nil
		}

		l.setConnectionState(ConnectionStateDisconnected)
		l.setConnected(false)

		// 6. Close participant channels to unblock any waiting goroutines
		l.logger.Info("Cleanup: closing participant channels", "room", l.room)
		close(l.participantAvailableCh)
		close(l.participantDisconnectCh)

		l.logger.Info("Cleanup: finished", "room", l.room)
	})

	return cleanupErr
}

// Reset reconnects to the room
func (l *LiveKitTransport) Reset() error {
	l.Cleanup()

	// Reset closeOnce so Cleanup can run again on the new connection
	l.closeOnce = sync.Once{}

	// Recreate channels (old ones are closed)
	l.participantAvailableCh = make(chan *lksdk.RemoteParticipant, 1)
	l.participantDisconnectCh = make(chan DisconnectReason, 1)

	l.mu.Lock()
	l.closed = false
	l.mu.Unlock()

	return l.Connect()
}

// GetLinkedParticipant returns the currently linked participant identity
func (l *LiveKitTransport) GetLinkedParticipant() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.linkedParticipant
}

// ParticipantDisconnectChan returns a channel that receives disconnect reasons
func (l *LiveKitTransport) ParticipantDisconnectChan() <-chan DisconnectReason {
	return l.participantDisconnectCh
}

// sendTranscription sends transcription data to the room
func (l *LiveKitTransport) sendTranscription(text string, isFinal bool, participant string) error {
	if !l.outputOptions.TranscriptionEnabled {
		return nil // Silently ignore if disabled
	}

	// Emit transcription event if channel is set
	if l.transcriptionChan != nil {
		event := TranscriptionEvent{
			Text:        text,
			Participant: participant,
			IsFinal:     isFinal,
		}
		select {
		case l.transcriptionChan <- event:
		case <-l.ctx.Done():
		}
	}

	// In a full implementation, this would use LiveKit's transcription API
	// For now, we send as a data packet with transcription topic
	if text == "" {
		return nil
	}

	payload := []byte(text)
	return l.client.LocalParticipant.PublishDataPacket(
		lksdk.UserData(payload),
		lksdk.WithDataPublishReliable(true),
		lksdk.WithDataPublishTopic("transcription"),
	)
}

// SetTranscriptionChannel sets the channel for transcription events
func (l *LiveKitTransport) SetTranscriptionChannel(ch chan<- TranscriptionEvent) {
	l.transcriptionChan = ch
}

// NotifySpeechStarted updates agent state when speech starts
func (l *LiveKitTransport) NotifySpeechStarted() {
	_ = l.setAgentState("speaking")
}

// NotifySpeechEnded updates agent state when speech ends
func (l *LiveKitTransport) NotifySpeechEnded() {
	_ = l.setAgentState("listening")
}

// NotifyThinking updates agent state when processing
func (l *LiveKitTransport) NotifyThinking() {
	_ = l.setAgentState("thinking")
}

// StartReceiving listens to remote tracks and data - implements core.TransportService
func (l *LiveKitTransport) StartReceiving(outputChan chan<- core.MediaChunk, errorChan chan<- error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.recvRunning {
		l.logger.Warn("receiving already running, replacing channels")
		// Update channels even if running
		l.outputChan = outputChan
		l.errorChan = errorChan
		return
	}

	l.recvRunning = true
	l.outputChan = outputChan
	l.errorChan = errorChan

	l.logger.Info("started receiving",
		"room", l.room,
		"audioEnabled", l.inputOptions.AudioEnabled,
		"videoEnabled", l.inputOptions.VideoEnabled,
	)

	// If we're already connected, log current participants to help debugging
	if l.client != nil {
		participants := l.client.GetRemoteParticipants()
		l.logger.Info("current participants when starting receive",
			"count", len(participants))
		for _, p := range participants {
			l.logger.Info("participant present",
				"identity", p.Identity(),
				"trackCount", len(p.TrackPublications()))
		}
	}

	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		// Wait for context cancellation or participant disconnect
		select {
		case <-l.ctx.Done():
			l.logger.Info("receiving stopped due to context done")
		case reason := <-l.participantDisconnectCh:
			l.logger.Info("participant disconnected, stopping receive",
				"reason", reason.String())
			if l.errorChan != nil {
				select {
				case l.errorChan <- fmt.Errorf("participant disconnected: %s", reason.String()):
				case <-l.ctx.Done():
				default:
				}
			}
		}
	}()
}

// -----------------------------------------------------------------------------
// SendEvent - Complete implementation of core.IEvent handling
// -----------------------------------------------------------------------------

// SendEvent implements core.ITransportService.SendEvent
func (l *LiveKitTransport) SendEvent(data core.IEvent) error {
	if !l.isConnected() {
		return errors.New("transport not connected")
	}

	l.mu.RLock()
	client := l.client
	l.mu.RUnlock()

	if client == nil {
		return errors.New("transport not connected")
	}
	if client.LocalParticipant == nil {
		return errors.New("local participant not initialized")
	}

	// Handle different event types
	switch e := data.(type) {
	// -------------------------------------------------------------------------
	// LLM Events
	// -------------------------------------------------------------------------
	case *llm.LLMGenerateResponseEvent:
		// Request to generate LLM response
		l.logger.Debug("received LLM generate response event")
		l.NotifyThinking()
		return l.sendAgentState("thinking")

	case *llm.LLMResponseStartedEvent:
		// LLM response generation started
		l.logger.Debug("LLM response started")
		l.NotifyThinking()
		return l.sendAgentState("thinking")

	case *llm.LLMResponseChunkEvent:
		// Chunk of LLM response text
		// If audio is enabled, this would typically go through TTS
		// For now, send as text message if text is not empty
		if e.Chunk != "" && l.outputOptions.TranscriptionEnabled {
			return l.sendTranscription(e.Chunk, false, l.GetLinkedParticipant())
		}
		return nil

	case *llm.LLMResponseCompletedEvent:
		// LLM response completed
		l.logger.Debug("LLM response completed", "length", len(e.FullText))
		l.NotifySpeechEnded()

		// Send final transcription
		if e.FullText != "" && l.outputOptions.TranscriptionEnabled {
			return l.sendTranscription(e.FullText, true, l.GetLinkedParticipant())
		}
		return l.sendAgentState("listening")

	case *llm.LLMToolInvocationRequestedEvent:
		// Tool invocation requested by LLM
		l.logger.Info("tool invocation requested",
			"toolId", e.ToolId,
			"hasParams", e.Params != nil,
		)

		// Forward tool invocation as a data packet for monitoring
		toolData, err := json.Marshal(map[string]interface{}{
			"type":   "tool_invocation",
			"toolId": e.ToolId,
			"params": e.Params,
		})
		if err != nil {
			return fmt.Errorf("failed to marshal tool invocation: %w", err)
		}

		return l.client.LocalParticipant.PublishDataPacket(
			lksdk.UserData(toolData),
			lksdk.WithDataPublishReliable(true),
			lksdk.WithDataPublishTopic("tools"),
		)

	// -------------------------------------------------------------------------
	// STT Events
	// -------------------------------------------------------------------------
	case *stt.STTInterimOutputEvent:
		// Interim transcription result
		if l.outputOptions.TranscriptionEnabled && e.Text != "" {
			l.logger.Debug("STT interim", "text", e.Text)
			return l.sendTranscription(e.Text, false, l.GetLinkedParticipant())
		}
		return nil

	case *stt.STTFinalOutputEvent:
		// Final transcription result
		if l.outputOptions.TranscriptionEnabled && e.Text != "" {
			l.logger.Debug("STT final", "text", e.Text)
			return l.sendTranscription(e.Text, true, l.GetLinkedParticipant())
		}
		return nil

	case *tts.TTSSpeakingStartedEvent:
		// TTS started speaking
		l.logger.Debug("TTS speaking started")
		l.NotifySpeechStarted()
		return l.sendAgentState("speaking")

	case *tts.TTSSpeakingEndedEvent:
		// TTS finished speaking
		l.logger.Debug("TTS speaking ended")
		l.NotifySpeechEnded()
		return l.sendAgentState("listening")

	case *tts.TTSOutputEvent:
		// TTS audio output - always in OPUS format from the pipeline
		if !l.outputOptions.AudioEnabled {
			return nil
		}
		if l.audioTrack == nil {
			return errors.New("audio track not initialized")
		}
		if e.AudioChunk.Data == nil || len(*e.AudioChunk.Data) == 0 {
			return nil
		}

		pcmChunk, err := audio.ConvertAudioChunk(
			e.AudioChunk,
			core.PCM, // Convert to PCM16 format
			l.outputOptions.AudioNumChannels,
			l.outputOptions.AudioSampleRate,
		)
		if err != nil {
			l.logger.Error("failed to convert audio chunk to PCM", "error", err)
			return err
		}
		pcmBytes := pcmChunk.Data
		// Convert PCM bytes to samples and write to audio track
		samples := bytesToPCM16(*pcmBytes)
		if err := l.audioTrack.WriteSample(samples); err != nil {
			return fmt.Errorf("failed to write audio sample: %w", err)
		}

		return nil

	// -------------------------------------------------------------------------
	// VAD Events
	// -------------------------------------------------------------------------
	case *vad.VadUserSpeechStartedEvent:
		// User started speaking
		l.logger.Debug("user speech started")
		l.NotifySpeechEnded() // Agent should stop speaking
		return l.sendAgentState("listening")

	case *vad.VadUserSpeechEndedEvent:
		// User finished speaking
		l.logger.Debug("user speech ended")
		l.NotifyThinking()
		return l.sendAgentState("thinking")

	case *vad.VadInterruptionSuspectedEvent:
		// User interrupted the agent
		l.logger.Info("interruption detected")
		l.NotifySpeechEnded()
		// clear audio track bufferr
		l.audioTrack.ClearQueue()
		return l.sendAgentState("listening")
	case *vad.VadInterruptionConfirmedEvent:
		// User interruption confirmed
		l.logger.Info("interruption confirmed")
		l.NotifySpeechEnded()
		// clear audio track buffer
		l.audioTrack.ClearQueue()
		return l.sendAgentState("listening")

	case *vad.VADUserSpeakingEvent:
		// Audio chunk with user speech - already handled via track reading
		return nil

	case *vad.VADSilenceEvent:
		// Silence chunk
		return nil

	// -------------------------------------------------------------------------
	// VectorDB Events
	// -------------------------------------------------------------------------
	case *vectordb.VectorDBQueryEvent:
		// Vector database query
		l.logger.Debug("vector DB query", "query", e.Query)

		// Forward query as data packet for monitoring
		queryData, err := json.Marshal(map[string]interface{}{
			"type":  "vectordb_query",
			"query": e.Query,
		})
		if err != nil {
			return fmt.Errorf("failed to marshal vector DB query: %w", err)
		}

		return l.client.LocalParticipant.PublishDataPacket(
			lksdk.UserData(queryData),
			lksdk.WithDataPublishReliable(true),
			lksdk.WithDataPublishTopic("vectordb"),
		)

	case *vectordb.VectorDBQueryResultEvent:
		// Vector database query results
		l.logger.Debug("vector DB query result", "resultCount", len(e.Results))

		if len(e.Results) == 0 {
			return nil
		}

		// Send first result as text message
		// In a production system, you'd want to send all results or process them
		payload := []byte(e.Results[0])
		return l.client.LocalParticipant.PublishDataPacket(
			lksdk.UserData(payload),
			lksdk.WithDataPublishReliable(true),
		)

	// -------------------------------------------------------------------------
	// Video Events
	// -------------------------------------------------------------------------
	case *video.VideoOutputEvent:
		// Video output event
		if !l.inputOptions.VideoEnabled {
			return nil
		}
		if e.VideoChunk.Data == nil || len(*e.VideoChunk.Data) == 0 {
			return nil
		}

		l.logger.Warn("video output not implemented in LiveKit transport",
			"size", len(*e.VideoChunk.Data),
			"format", e.VideoChunk.Format,
		)
		return errors.New("video output not implemented")

	default:
		return nil
	}
}

// -----------------------------------------------------------------------------
// Helper Methods for SendEvent
// -----------------------------------------------------------------------------

// sendAgentState sends agent state as a data packet and sets attributes
func (l *LiveKitTransport) sendAgentState(state string) error {
	// Set local attributes
	if err := l.setAgentState(state); err != nil {
		return err
	}

	// Also send as a data packet for clients that monitor state via data channel
	if l.client != nil && l.client.LocalParticipant != nil {
		stateMsg := fmt.Sprintf(`{"type":"agent_state","state":"%s"}`, state)
		return l.client.LocalParticipant.PublishDataPacket(
			lksdk.UserData([]byte(stateMsg)),
			lksdk.WithDataPublishReliable(true),
			lksdk.WithDataPublishTopic("agent_state"),
		)
	}
	return nil
}

// SendAgentMessage sends a text message to the linked participant
func (l *LiveKitTransport) SendAgentMessage(text string) error {
	if !l.connected || l.client == nil {
		return errors.New("transport not connected")
	}
	if l.client.LocalParticipant == nil {
		return errors.New("local participant not initialized")
	}

	payload := []byte(text)
	return l.client.LocalParticipant.PublishDataPacket(
		lksdk.UserData(payload),
		lksdk.WithDataPublishReliable(true),
	)
}

// SendTranscription implements sending transcription events
func (l *LiveKitTransport) SendTranscription(text string, isFinal bool) error {
	return l.sendTranscription(text, isFinal, l.GetLinkedParticipant())
}

// -----------------------------------------------------------------------------
// Utility Functions
// -----------------------------------------------------------------------------

// bytesToPCM16 converts raw bytes (little-endian int16) to PCM16Sample
func bytesToPCM16(data []byte) media.PCM16Sample {
	samples := make(media.PCM16Sample, len(data)/2)
	for i := 0; i < len(samples); i++ {
		samples[i] = int16(binary.LittleEndian.Uint16(data[i*2:]))
	}
	return samples
}

// randomString generates a random string of the specified length
func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
		time.Sleep(time.Nanosecond)
	}
	return string(b)
}
