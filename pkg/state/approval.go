// Package state provides approval state management for command execution requests.
// Approvals are stored as JSON files under <configDir>/approvals/ with atomic write
// operations and cross-process file locking.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/adryanev/orkestra/pkg/risk"
	"github.com/google/uuid"
)

// ApprovalRequest represents a pending command approval request from an agent.
type ApprovalRequest struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspace_id"`
	Agent       string     `json:"agent"` // claude or codex
	Command     string     `json:"command"`
	RiskLevel   risk.Level `json:"risk_level"`
	RequestedAt time.Time  `json:"requested_at"`
	TimeoutAt   time.Time  `json:"timeout_at"`
}

// ApprovalResponse represents the user's response to an approval request.
type ApprovalResponse struct {
	RequestID   string    `json:"request_id"`
	Approved    bool      `json:"approved"`
	RespondedAt time.Time `json:"responded_at"`
	RespondedBy string    `json:"responded_by"` // user identifier
}

// NewApprovalRequest creates a new approval request with a generated ID.
func NewApprovalRequest(workspaceID, agent, command string, riskLevel risk.Level, timeout time.Duration) ApprovalRequest {
	now := time.Now()
	return ApprovalRequest{
		ID:          uuid.New().String(),
		WorkspaceID: workspaceID,
		Agent:       agent,
		Command:     command,
		RiskLevel:   riskLevel,
		RequestedAt: now,
		TimeoutAt:   now.Add(timeout),
	}
}

// approvalDir returns the directory for approval state files under configDir.
func approvalDir(configDir string) string {
	return filepath.Join(configDir, "approvals")
}

// pendingPath returns the path to the pending approval file for a workspace.
func pendingPath(configDir, workspaceID string) string {
	return filepath.Join(approvalDir(configDir), workspaceID+"_pending.json")
}

// responsePath returns the path to the approval response file for a workspace.
func responsePath(configDir, workspaceID string) string {
	return filepath.Join(approvalDir(configDir), workspaceID+"_response.json")
}

// lockPath returns the path to the lock file for approval state.
func lockPath(configDir, workspaceID string) string {
	return filepath.Join(approvalDir(configDir), workspaceID+".lock")
}

// WritePendingApproval atomically writes a pending approval request.
// It acquires a file lock to ensure thread-safety across processes.
// configDir is the root Orkestra state directory (e.g. Manager.ConfigDir()).
func WritePendingApproval(configDir, workspaceID string, req ApprovalRequest) error {
	lock, err := Acquire(lockPath(configDir, workspaceID))
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer func() { _ = lock.Release() }()

	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	if err := WriteAtomic(pendingPath(configDir, workspaceID), data, 0600); err != nil {
		return fmt.Errorf("write pending approval: %w", err)
	}

	return nil
}

// ReadPendingApproval reads a pending approval request.
// Returns nil if no pending request exists (not an error).
// configDir is the root Orkestra state directory (e.g. Manager.ConfigDir()).
func ReadPendingApproval(configDir, workspaceID string) (*ApprovalRequest, error) {
	lock, err := Acquire(lockPath(configDir, workspaceID))
	if err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	defer func() { _ = lock.Release() }()

	data, err := ReadFile(pendingPath(configDir, workspaceID))
	if err != nil {
		return nil, fmt.Errorf("read pending approval: %w", err)
	}
	if data == nil {
		return nil, nil
	}

	var req ApprovalRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("unmarshal request: %w", err)
	}

	return &req, nil
}

// WriteApprovalResponse atomically writes an approval response.
// It acquires a file lock to ensure thread-safety across processes.
// configDir is the root Orkestra state directory (e.g. Manager.ConfigDir()).
func WriteApprovalResponse(configDir, workspaceID string, resp ApprovalResponse) error {
	lock, err := Acquire(lockPath(configDir, workspaceID))
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer func() { _ = lock.Release() }()

	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}

	if err := WriteAtomic(responsePath(configDir, workspaceID), data, 0600); err != nil {
		return fmt.Errorf("write approval response: %w", err)
	}

	return nil
}

// ReadApprovalResponse reads an approval response.
// Returns nil if no response exists (not an error).
// configDir is the root Orkestra state directory (e.g. Manager.ConfigDir()).
func ReadApprovalResponse(configDir, workspaceID string) (*ApprovalResponse, error) {
	lock, err := Acquire(lockPath(configDir, workspaceID))
	if err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	defer func() { _ = lock.Release() }()

	data, err := ReadFile(responsePath(configDir, workspaceID))
	if err != nil {
		return nil, fmt.Errorf("read approval response: %w", err)
	}
	if data == nil {
		return nil, nil
	}

	var resp ApprovalResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &resp, nil
}

// CleanupApprovalState removes both pending and response files for a workspace.
// This should be called after an approval workflow completes.
// Returns nil if files don't exist (not an error).
// configDir is the root Orkestra state directory (e.g. Manager.ConfigDir()).
func CleanupApprovalState(configDir, workspaceID string) error {
	lock, err := Acquire(lockPath(configDir, workspaceID))
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer func() { _ = lock.Release() }()

	// Remove pending file (ignore not-exist errors)
	if err := os.Remove(pendingPath(configDir, workspaceID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove pending file: %w", err)
	}

	// Remove response file (ignore not-exist errors)
	if err := os.Remove(responsePath(configDir, workspaceID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove response file: %w", err)
	}

	return nil
}
