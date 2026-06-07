package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adryanev/orkestra/pkg/state"
)

// PendingQuestion represents a question the agent asked via ask_user that is
// waiting for an answer from the orchestrating user. Written atomically to
// ~/.orkestra/pending/<workspace-id>.json by the MCP tool handler.
type PendingQuestion struct {
	WorkspaceID string    `json:"workspace_id"`
	Question    string    `json:"question"`
	Options     []string  `json:"options,omitempty"`
	AskedAt     time.Time `json:"asked_at"`
}

// AnswerRecord is an audit trail entry for a question that was answered.
// Appended to ~/.orkestra/answers/<workspace-id>.json by orkestra resume --answer.
type AnswerRecord struct {
	WorkspaceID string    `json:"workspace_id"`
	Question    string    `json:"question"`
	Options     []string  `json:"options,omitempty"`
	Answer      string    `json:"answer"`
	AskedAt     time.Time `json:"asked_at"`
	AnsweredAt  time.Time `json:"answered_at"`
}

// pendingPath builds the path to the pending-question file for a workspace.
// It validates that workspaceID contains no path separators or ".." segments
// to prevent path traversal attacks (same guard as notify.go).
func pendingPath(configDir, workspaceID string) (string, error) {
	if strings.ContainsAny(workspaceID, `/\`) || strings.Contains(workspaceID, "..") {
		return "", fmt.Errorf("invalid workspace id %q: contains path separators or ..", workspaceID)
	}
	return filepath.Join(configDir, "pending", workspaceID+".json"), nil
}

// answerPath builds the path to the answer audit file for a workspace.
// It validates that workspaceID contains no path separators or ".." segments.
func answerPath(configDir, workspaceID string) (string, error) {
	if strings.ContainsAny(workspaceID, `/\`) || strings.Contains(workspaceID, "..") {
		return "", fmt.Errorf("invalid workspace id %q: contains path separators or ..", workspaceID)
	}
	return filepath.Join(configDir, "answers", workspaceID+".json"), nil
}

// WritePending atomically writes a pending question to disk. Returns an error
// if a pending file already exists for this workspace (R4 — only one pending
// question allowed at a time).
func WritePending(configDir, workspaceID string, q PendingQuestion) error {
	path, err := pendingPath(configDir, workspaceID)
	if err != nil {
		return err
	}

	// R4: reject if pending file already exists
	if data, _ := state.ReadFile(path); data != nil {
		return fmt.Errorf("pending question already exists for workspace %s", workspaceID)
	}

	data, err := json.MarshalIndent(q, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal pending question: %w", err)
	}

	if err := state.WriteAtomic(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write pending file: %w", err)
	}
	return nil
}

// ReadPending reads the pending question for a workspace. Returns (nil, nil)
// when no pending file exists (same pattern as state.ReadFile).
func ReadPending(configDir, workspaceID string) (*PendingQuestion, error) {
	path, err := pendingPath(configDir, workspaceID)
	if err != nil {
		return nil, err
	}

	data, err := state.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read pending file: %w", err)
	}
	if data == nil {
		return nil, nil
	}

	var q PendingQuestion
	if err := json.Unmarshal(data, &q); err != nil {
		return nil, fmt.Errorf("failed to unmarshal pending question: %w", err)
	}
	return &q, nil
}

// DeletePending removes the pending question file for a workspace. Idempotent:
// returns nil if the file doesn't exist (not an error condition).
func DeletePending(configDir, workspaceID string) error {
	path, err := pendingPath(configDir, workspaceID)
	if err != nil {
		return err
	}

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete pending file: %w", err)
	}
	return nil
}

// AppendAnswer appends an answer record to the audit trail. Reads the existing
// file (empty slice if absent), appends the new record, and writes atomically.
// No lock needed because Hermes calls resume sequentially per workspace.
func AppendAnswer(configDir, workspaceID string, r AnswerRecord) error {
	path, err := answerPath(configDir, workspaceID)
	if err != nil {
		return err
	}

	// Read existing records (nil data means empty file → empty slice)
	var records []AnswerRecord
	if data, err := state.ReadFile(path); err != nil {
		return fmt.Errorf("failed to read answer file: %w", err)
	} else if data != nil {
		if err := json.Unmarshal(data, &records); err != nil {
			return fmt.Errorf("failed to unmarshal answer file: %w", err)
		}
	}

	// Append new record
	records = append(records, r)

	// Write atomically
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal answer records: %w", err)
	}

	if err := state.WriteAtomic(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write answer file: %w", err)
	}
	return nil
}
