package daily

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DailyAPIClient wraps Daily's REST API.
type DailyAPIClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// RoomConfig is the request body for POST /rooms.
type RoomConfig struct {
	Name       string          `json:"name,omitempty"`
	Privacy    string          `json:"privacy,omitempty"`
	Properties *RoomProperties `json:"properties,omitempty"`
}

// RoomProperties configures room behaviour.
type RoomProperties struct {
	MaxParticipants int   `json:"max_participants,omitempty"`
	ExpiresAt       int64 `json:"exp,omitempty"`
	StartVideoOff   bool  `json:"start_video_off,omitempty"`
	StartAudioOff   bool  `json:"start_audio_off,omitempty"`
}

// Room is the response from POST /rooms.
type Room struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	URL        string          `json:"url"`
	APICreated bool            `json:"api_created"`
	Privacy    string          `json:"privacy"`
	Properties *RoomProperties `json:"config,omitempty"`
	CreatedAt  string          `json:"created_at"`
}

// MeetingTokenConfig is the request body for POST /meeting-tokens.
type MeetingTokenConfig struct {
	Properties *MeetingTokenProperties `json:"properties"`
}

// MeetingTokenProperties configures token permissions.
type MeetingTokenProperties struct {
	RoomName  string `json:"room_name"`
	UserName  string `json:"user_name,omitempty"`
	IsOwner   bool   `json:"is_owner,omitempty"`
	ExpiresAt int64  `json:"exp,omitempty"`
}

// MeetingToken is the response from POST /meeting-tokens.
type MeetingToken struct {
	Token string `json:"token"`
}

// NewDailyAPIClient creates a new API client.
func NewDailyAPIClient(apiKey, baseURL string) *DailyAPIClient {
	if baseURL == "" {
		baseURL = "https://api.daily.co/v1"
	}
	return &DailyAPIClient{
		apiKey:  apiKey,
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// CreateRoom creates a new Daily room.
func (c *DailyAPIClient) CreateRoom(config RoomConfig) (*Room, error) {
	body, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal room config: %w", err)
	}

	req, err := http.NewRequest("POST", c.baseURL+"/rooms", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("create room request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("create room: status %d: %s", resp.StatusCode, string(respBody))
	}

	var room Room
	if err := json.NewDecoder(resp.Body).Decode(&room); err != nil {
		return nil, fmt.Errorf("decode room response: %w", err)
	}
	return &room, nil
}

// CreateMeetingToken generates a meeting token for a room.
func (c *DailyAPIClient) CreateMeetingToken(config MeetingTokenConfig) (string, error) {
	body, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("marshal token config: %w", err)
	}

	req, err := http.NewRequest("POST", c.baseURL+"/meeting-tokens", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create token: status %d: %s", resp.StatusCode, string(respBody))
	}

	var token MeetingToken
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	return token.Token, nil
}

// DeleteRoom deletes a Daily room.
func (c *DailyAPIClient) DeleteRoom(roomName string) error {
	req, err := http.NewRequest("DELETE", c.baseURL+"/rooms/"+roomName, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete room request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete room: status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// GetRoom retrieves room details.
func (c *DailyAPIClient) GetRoom(roomName string) (*Room, error) {
	req, err := http.NewRequest("GET", c.baseURL+"/rooms/"+roomName, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get room request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get room: status %d: %s", resp.StatusCode, string(respBody))
	}

	var room Room
	if err := json.NewDecoder(resp.Body).Decode(&room); err != nil {
		return nil, fmt.Errorf("decode room response: %w", err)
	}
	return &room, nil
}

func (c *DailyAPIClient) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
}
