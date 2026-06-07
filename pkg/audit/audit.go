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

// auditLogPath returns the path to the audit log file.
func auditLogPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	dir := filepath.Join(home, ".orkestra", "audit")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create audit dir: %w", err)
	}
	return filepath.Join(dir, "approvals.jsonl"), nil
}

// LogRequest logs an approval request event.
func LogRequest(workspaceID, agent, command, requestID string, riskLevel risk.Level) error {
	event := Event{
		Timestamp:   time.Now(),
		WorkspaceID: workspaceID,
		Agent:       agent,
		Command:     command,
		RiskLevel:   riskLevel,
		EventType:   "request",
		RequestID:   requestID,
	}
	return logEvent(event)
}

// LogApprove logs an approval response event.
func LogApprove(workspaceID, agent, command, requestID, respondedBy string, riskLevel risk.Level) error {
	event := Event{
		Timestamp:   time.Now(),
		WorkspaceID: workspaceID,
		Agent:       agent,
		Command:     command,
		RiskLevel:   riskLevel,
		EventType:   "approve",
		RequestID:   requestID,
		RespondedBy: respondedBy,
	}
	return logEvent(event)
}

// LogReject logs a rejection response event.
func LogReject(workspaceID, agent, command, requestID, respondedBy string, riskLevel risk.Level) error {
	event := Event{
		Timestamp:   time.Now(),
		WorkspaceID: workspaceID,
		Agent:       agent,
		Command:     command,
		RiskLevel:   riskLevel,
		EventType:   "reject",
		RequestID:   requestID,
		RespondedBy: respondedBy,
	}
	return logEvent(event)
}

// LogTimeout logs a timeout event.
func LogTimeout(workspaceID, agent, command, requestID string, riskLevel risk.Level) error {
	event := Event{
		Timestamp:   time.Now(),
		WorkspaceID: workspaceID,
		Agent:       agent,
		Command:     command,
		RiskLevel:   riskLevel,
		EventType:   "timeout",
		RequestID:   requestID,
	}
	return logEvent(event)
}

// logEvent appends an event to the audit log.
func logEvent(event Event) error {
	path, err := auditLogPath()
	if err != nil {
		return err
	}

	// Open file in append mode
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	defer f.Close()

	// Marshal event to JSON
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	// Append newline-delimited JSON
	data = append(data, '\n')
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}

	return nil
}
