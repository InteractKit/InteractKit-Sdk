package protocol

import (
	"encoding/json"
	"fmt"
)

// Marshal creates a JSON-encoded Envelope from a message type and payload.
func Marshal(msgType MessageType, payload interface{}) ([]byte, error) {
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("protocol: marshal payload for %q: %w", msgType, err)
		}
		raw = b
	}
	return json.Marshal(Envelope{
		Type:    msgType,
		Payload: raw,
	})
}

// Unmarshal parses a JSON-encoded Envelope, returning the message type and raw payload.
func Unmarshal(data []byte) (MessageType, json.RawMessage, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return "", nil, fmt.Errorf("protocol: unmarshal envelope: %w", err)
	}
	if env.Type == "" {
		return "", nil, fmt.Errorf("protocol: envelope missing type field")
	}
	return env.Type, env.Payload, nil
}

// UnmarshalPayload decodes a raw JSON payload into a typed struct.
func UnmarshalPayload[T any](raw json.RawMessage) (T, error) {
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, fmt.Errorf("protocol: unmarshal payload: %w", err)
	}
	return v, nil
}
