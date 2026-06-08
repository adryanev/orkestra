// Package audit provides structured audit logging for approval requests and responses.
package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/adryanev/orkestra/pkg/risk"
)

// Event represents an audit log entry.
type Event struct {
	Timestamp   time.Time  `json:"timestamp"`
	WorkspaceID string     `json:"workspace_id"`
	Agent       string     `json:"agent"`
	Command     string     `json:"command"`
	RiskLevel   risk.Level `json:"risk_level"`
	EventType   string     `json:"event_type"` // "request", "approve", "reject", "timeout"
	RequestID   string     `json:"request_id,omitempty"`
	RespondedBy string     `json:"responded_by,omitempty"`
}

// auditLogPath returns the path to the audit log file under configDir.
func auditLogPath(configDir string) (string, error) {
	dir := filepath.Join(configDir, "audit")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create audit dir: %w", err)
	}
	return filepath.Join(dir, "approvals.jsonl"), nil
}

// LogRequest logs an approval request event.
// configDir is the root Orkestra state directory (e.g. Manager.ConfigDir()).
func LogRequest(configDir, workspaceID, agent, command, requestID string, riskLevel risk.Level) error {
	return logEvent(configDir, Event{
		Timestamp:   time.Now(),
		WorkspaceID: workspaceID,
		Agent:       agent,
		Command:     command,
		RiskLevel:   riskLevel,
		EventType:   "request",
		RequestID:   requestID,
	})
}

// LogApprove logs an approval response event.
// configDir is the root Orkestra state directory (e.g. Manager.ConfigDir()).
func LogApprove(configDir, workspaceID, agent, command, requestID, respondedBy string, riskLevel risk.Level) error {
	return logEvent(configDir, Event{
		Timestamp:   time.Now(),
		WorkspaceID: workspaceID,
		Agent:       agent,
		Command:     command,
		RiskLevel:   riskLevel,
		EventType:   "approve",
		RequestID:   requestID,
		RespondedBy: respondedBy,
	})
}

// LogReject logs a rejection response event.
// configDir is the root Orkestra state directory (e.g. Manager.ConfigDir()).
func LogReject(configDir, workspaceID, agent, command, requestID, respondedBy string, riskLevel risk.Level) error {
	return logEvent(configDir, Event{
		Timestamp:   time.Now(),
		WorkspaceID: workspaceID,
		Agent:       agent,
		Command:     command,
		RiskLevel:   riskLevel,
		EventType:   "reject",
		RequestID:   requestID,
		RespondedBy: respondedBy,
	})
}

// LogTimeout logs a timeout event.
// configDir is the root Orkestra state directory (e.g. Manager.ConfigDir()).
func LogTimeout(configDir, workspaceID, agent, command, requestID string, riskLevel risk.Level) error {
	return logEvent(configDir, Event{
		Timestamp:   time.Now(),
		WorkspaceID: workspaceID,
		Agent:       agent,
		Command:     command,
		RiskLevel:   riskLevel,
		EventType:   "timeout",
		RequestID:   requestID,
	})
}

// logEvent appends an event to the audit log under configDir.
func logEvent(configDir string, event Event) (retErr error) {
	path, err := auditLogPath(configDir)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = f.Close()
		return fmt.Errorf("chmod audit log: %w", err)
	}
	defer func() {
		if cerr := f.Close(); retErr == nil && cerr != nil {
			retErr = fmt.Errorf("close audit log: %w", cerr)
		}
	}()

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	data = append(data, '\n')
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}

	return nil
}
