package pty

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// Message types for PTY protocol.
const (
	// Client -> Server
	MsgAttach = "attach" // Client wants to attach to PTY
	MsgInput  = "input"  // Client sends input data
	MsgResize = "resize" // Client requests terminal resize
	MsgDetach = "detach" // Client detaches from PTY

	// Server -> Client
	MsgReady  = "ready"  // Server confirms attachment/ready state
	MsgBuffer = "buffer" // Server sends buffered history
	MsgOutput = "output" // Server sends PTY output
	MsgExit   = "exit"   // PTY process exited
	MsgError  = "error"  // Error occurred
)

// Msg represents a protocol message exchanged between client and server.
type Msg struct {
	Type    string `json:"type"`              // Message type (one of Msg* constants)
	Data    string `json:"data,omitempty"`    // Base64-encoded data (for input/output/buffer)
	Rows    uint16 `json:"rows,omitempty"`    // Terminal rows (for resize)
	Cols    uint16 `json:"cols,omitempty"`    // Terminal columns (for resize)
	Code    *int   `json:"code,omitempty"`    // Exit code (for exit)
	Message string `json:"message,omitempty"` // Human-readable message (for error/ready)
}

// Encode serializes a message to JSON followed by a newline.
func Encode(m Msg) ([]byte, error) {
	if m.Type == "" {
		return nil, fmt.Errorf("message type cannot be empty")
	}

	data, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal message: %w", err)
	}

	// Append newline for line-delimited JSON
	result := append(data, '\n')
	return result, nil
}

// Decode parses a JSON line into a message.
func Decode(line []byte) (Msg, error) {
	if len(line) == 0 {
		return Msg{}, fmt.Errorf("empty message")
	}

	var m Msg
	if err := json.Unmarshal(line, &m); err != nil {
		return Msg{}, fmt.Errorf("failed to unmarshal message: %w", err)
	}

	if m.Type == "" {
		return Msg{}, fmt.Errorf("message missing type field")
	}

	return m, nil
}

// EncodeData base64-encodes binary data for inclusion in a message.
func EncodeData(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// DecodeData base64-decodes data from a message.
func DecodeData(encoded string) ([]byte, error) {
	if encoded == "" {
		return nil, nil
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 data: %w", err)
	}
	return data, nil
}
