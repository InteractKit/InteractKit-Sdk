package daily

import (
	"context"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"interactkit/core"
	"interactkit/events/llm"
	"interactkit/events/tts"
	"interactkit/events/vad"
	"interactkit/utils/audio"

	"github.com/gorilla/websocket"
)

// DailyMediaMessage represents a message exchanged over the audio relay WebSocket.
type DailyMediaMessage struct {
	Type       string `json:"type"`                 // "audio", "text", "control"
	Payload    string `json:"payload,omitempty"`     // Base64-encoded audio data or text
	Timestamp  int64  `json:"timestamp,omitempty"`   // Unix milliseconds
	Room       string `json:"room,omitempty"`        // Room name
	SampleRate int    `json:"sample_rate,omitempty"` // Audio sample rate
	Channels   int    `json:"channels,omitempty"`    // Audio channels
	Format     string `json:"format,omitempty"`      // "pcm16", "opus"
	Action     string `json:"action,omitempty"`      // Control action
}

// DailyTransportService implements transport.ITransportService for Daily.co.
type DailyTransportService struct {
	conn     *websocket.Conn
	config   *Config
	roomName string
	logger   *core.Logger

	mu        sync.RWMutex
	writeMu   sync.Mutex
	connected bool
	closeOnce sync.Once

	ctx    context.Context
	cancel context.CancelFunc
}

// NewDailyTransportService creates a new Daily transport service.
func NewDailyTransportService(
	conn *websocket.Conn,
	config *Config,
	roomName string,
	logger *core.Logger,
) *DailyTransportService {
	return &DailyTransportService{
		conn:      conn,
		config:    config,
		roomName:  roomName,
		logger:    logger.With(map[string]interface{}{"room": roomName, "component": "daily-service"}),
		connected: true,
	}
}

// Initialize implements core.IService.
func (s *DailyTransportService) Initialize(ctx context.Context) error {
	s.ctx, s.cancel = context.WithCancel(ctx)
	return nil
}

// Connect implements transport.ITransportService.
func (s *DailyTransportService) Connect() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.connected {
		return nil
	}

	s.connected = true
	s.logger.Info("Daily transport service connected")
	return nil
}

// StartReceiving implements transport.ITransportService.
func (s *DailyTransportService) StartReceiving(outputChan chan<- core.MediaChunk, errorChan chan<- error) {
	defer s.Close()

	s.logger.Info("started receiving audio from Daily relay")

	for {
		if s.ctx != nil {
			select {
			case <-s.ctx.Done():
				return
			default:
			}
		}

		var msg DailyMediaMessage
		err := s.conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseAbnormalClosure,
				websocket.CloseNormalClosure,
			) {
				errorChan <- fmt.Errorf("daily websocket error: %w", err)
			}
			s.logger.With(map[string]interface{}{"error": err}).Info("websocket read ended")
			return
		}

		switch msg.Type {
		case "audio":
			s.handleAudioMessage(&msg, outputChan)
		case "text":
			s.handleTextMessage(&msg, outputChan)
		case "control":
			s.handleControlMessage(&msg)
		}
	}
}

func (s *DailyTransportService) handleAudioMessage(msg *DailyMediaMessage, outputChan chan<- core.MediaChunk) {
	if msg.Payload == "" {
		return
	}

	audioData, err := base64.StdEncoding.DecodeString(msg.Payload)
	if err != nil {
		s.logger.With(map[string]interface{}{"error": err}).Error("failed to decode audio payload")
		return
	}

	format := core.PCM
	sampleRate := s.config.AudioSampleRate
	channels := s.config.AudioChannels

	if msg.SampleRate > 0 {
		sampleRate = msg.SampleRate
	}
	if msg.Channels > 0 {
		channels = msg.Channels
	}
	if msg.Format == "opus" {
		format = core.OPUS
	}

	chunk := core.MediaChunk{
		Audio: core.AudioChunk{
			Data:       &audioData,
			SampleRate: sampleRate,
			Channels:   channels,
			Format:     format,
			Timestamp:  time.Now(),
		},
	}

	select {
	case outputChan <- chunk:
	default:
		s.logger.Debug("output channel full, dropping audio chunk")
	}
}

func (s *DailyTransportService) handleTextMessage(msg *DailyMediaMessage, outputChan chan<- core.MediaChunk) {
	if msg.Payload == "" {
		return
	}

	chunk := core.MediaChunk{
		Text: core.TextChunk{
			Text: msg.Payload,
		},
	}

	select {
	case outputChan <- chunk:
	default:
	}
}

func (s *DailyTransportService) handleControlMessage(msg *DailyMediaMessage) {
	switch msg.Action {
	case "stop":
		s.logger.Info("received stop control message")
		s.Close()
	case "mute":
		s.logger.Info("participant muted")
	case "unmute":
		s.logger.Info("participant unmuted")
	default:
		s.logger.With(map[string]interface{}{"action": msg.Action}).Debug("unknown control action")
	}
}

// SendEvent implements transport.ITransportService.
func (s *DailyTransportService) SendEvent(data core.IEvent) error {
	s.mu.RLock()
	if !s.connected {
		s.mu.RUnlock()
		return fmt.Errorf("transport service not connected")
	}
	s.mu.RUnlock()

	switch e := data.(type) {
	case *tts.TTSOutputEvent:
		if e.AudioChunk.Data == nil || len(*e.AudioChunk.Data) == 0 {
			return nil
		}

		pcmChunk, err := audio.ConvertAudioChunk(
			e.AudioChunk,
			core.PCM,
			s.config.AudioChannels,
			s.config.AudioSampleRate,
		)
		if err != nil {
			s.logger.With(map[string]interface{}{"error": err}).Error("failed to convert audio")
			return err
		}

		encoded := base64.StdEncoding.EncodeToString(*pcmChunk.Data)
		return s.writeJSON(DailyMediaMessage{
			Type:       "audio",
			Payload:    encoded,
			Timestamp:  time.Now().UnixMilli(),
			SampleRate: s.config.AudioSampleRate,
			Channels:   s.config.AudioChannels,
			Format:     "pcm16",
		})

	case *tts.TTSSpeakingStartedEvent:
		return s.sendControl("speaking_started")

	case *tts.TTSSpeakingEndedEvent:
		return s.sendControl("speaking_ended")

	case *llm.LLMResponseChunkEvent:
		if e.Chunk != "" {
			return s.sendText("transcription", e.Chunk)
		}
		return nil

	case *llm.LLMResponseCompletedEvent:
		if e.FullText != "" {
			return s.sendText("transcription_final", e.FullText)
		}
		return nil

	case *llm.LLMGenerateResponseEvent:
		return s.sendControl("thinking")

	case *llm.LLMResponseStartedEvent:
		return s.sendControl("thinking")

	case *vad.VadUserSpeechStartedEvent:
		return s.sendControl("user_speech_started")

	case *vad.VadUserSpeechEndedEvent:
		return s.sendControl("user_speech_ended")

	case *vad.VadInterruptionSuspectedEvent:
		return s.sendControl("interruption")

	case *vad.VadInterruptionConfirmedEvent:
		return s.sendControl("interruption_confirmed")

	case *vad.VADUserSpeakingEvent,
		*vad.VADSilenceEvent:
		return nil

	default:
		return nil
	}
}

func (s *DailyTransportService) sendControl(action string) error {
	return s.writeJSON(DailyMediaMessage{
		Type:      "control",
		Action:    action,
		Timestamp: time.Now().UnixMilli(),
		Room:      s.roomName,
	})
}

func (s *DailyTransportService) sendText(msgType, text string) error {
	return s.writeJSON(DailyMediaMessage{
		Type:      msgType,
		Payload:   text,
		Timestamp: time.Now().UnixMilli(),
		Room:      s.roomName,
	})
}

func (s *DailyTransportService) writeJSON(msg DailyMediaMessage) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WriteJSON(msg)
}

// Cleanup implements core.IService.
func (s *DailyTransportService) Cleanup() error {
	return s.Close()
}

// Reset implements core.IService.
func (s *DailyTransportService) Reset() error {
	return nil
}

// Close closes the WebSocket connection.
func (s *DailyTransportService) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		s.connected = false

		if s.cancel != nil {
			s.cancel()
		}

		if s.conn != nil {
			s.conn.Close()
		}

		s.logger.Info("Daily transport service closed")
	})
	return nil
}

// GetRoomName returns the room name for this session.
func (s *DailyTransportService) GetRoomName() string {
	return s.roomName
}
