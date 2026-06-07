// Package state provides approval state management for command execution requests.
// Approvals are stored as JSON files in ~/.orkestra/approvals/ with atomic write
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
	ID          string      `json:"id"`
	WorkspaceID string      `json:"workspace_id"`
	Agent       string      `json:"agent"` // claude or codex
	Command     string      `json:"command"`
	RiskLevel   risk.Level  `json:"risk_level"`
	RequestedAt time.Time   `json:"requested_at"`
	TimeoutAt   time.Time   `json:"timeout_at"`
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

// approvalDir returns the directory for approval state files.
func approvalDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	return filepath.Join(home, ".orkestra", "approvals"), nil
}

// pendingPath returns the path to the pending approval file for a workspace.
func pendingPath(workspaceID string) (string, error) {
	dir, err := approvalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, workspaceID+"_pending.json"), nil
}

// responsePath returns the path to the approval response file for a workspace.
func responsePath(workspaceID string) (string, error) {
	dir, err := approvalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, workspaceID+"_response.json"), nil
}

// lockPath returns the path to the lock file for approval state.
func lockPath(workspaceID string) (string, error) {
	dir, err := approvalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, workspaceID+".lock"), nil
}

// WritePendingApproval atomically writes a pending approval request.
// It acquires a file lock to ensure thread-safety across processes.
func WritePendingApproval(workspaceID string, req ApprovalRequest) error {
	lockFile, err := lockPath(workspaceID)
	if err != nil {
		return err
	}

	lock, err := Acquire(lockFile)
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer lock.Release()

	path, err := pendingPath(workspaceID)
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	if err := WriteAtomic(path, data, 0644); err != nil {
		return fmt.Errorf("write pending approval: %w", err)
	}

	return nil
}

// ReadPendingApproval reads a pending approval request.
// Returns nil if no pending request exists (not an error).
func ReadPendingApproval(workspaceID string) (*ApprovalRequest, error) {
	lockFile, err := lockPath(workspaceID)
	if err != nil {
		return nil, err
	}

	lock, err := Acquire(lockFile)
	if err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	defer lock.Release()

	path, err := pendingPath(workspaceID)
	if err != nil {
		return nil, err
	}

	data, err := ReadFile(path)
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
func WriteApprovalResponse(workspaceID string, resp ApprovalResponse) error {
	lockFile, err := lockPath(workspaceID)
	if err != nil {
		return err
	}

	lock, err := Acquire(lockFile)
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer lock.Release()

	path, err := responsePath(workspaceID)
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}

	if err := WriteAtomic(path, data, 0644); err != nil {
		return fmt.Errorf("write approval response: %w", err)
	}

	return nil
}

// ReadApprovalResponse reads an approval response.
// Returns nil if no response exists (not an error).
func ReadApprovalResponse(workspaceID string) (*ApprovalResponse, error) {
	lockFile, err := lockPath(workspaceID)
	if err != nil {
		return nil, err
	}

	lock, err := Acquire(lockFile)
	if err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	defer lock.Release()

	path, err := responsePath(workspaceID)
	if err != nil {
		return nil, err
	}

	data, err := ReadFile(path)
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
func CleanupApprovalState(workspaceID string) error {
	lockFile, err := lockPath(workspaceID)
	if err != nil {
		return err
	}

	lock, err := Acquire(lockFile)
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer lock.Release()

	pending, err := pendingPath(workspaceID)
	if err != nil {
		return err
	}

	response, err := responsePath(workspaceID)
	if err != nil {
		return err
	}

	// Remove pending file (ignore not-exist errors)
	if err := os.Remove(pending); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove pending file: %w", err)
	}

	// Remove response file (ignore not-exist errors)
	if err := os.Remove(response); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove response file: %w", err)
	}

	return nil
}
