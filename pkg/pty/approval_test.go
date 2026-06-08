//go:build !windows

package pty

import (
	"bytes"
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/adryanev/orkestra/pkg/risk"
	"github.com/adryanev/orkestra/pkg/state"
)

func TestProcessWithPromptDetection(t *testing.T) {
	configDir := t.TempDir()

	tests := []struct {
		name           string
		input          string
		expectDetected bool
		expectedAgent  string
		expectedCmd    string
	}{
		{
			name:           "Claude bash command prompt",
			input:          "Allow claude to execute this Bash command? git status\n",
			expectDetected: true,
			expectedAgent:  "claude",
			expectedCmd:    "git status",
		},
		{
			name:           "Claude MCP tool prompt",
			input:          "Allow claude to call mcp__orkestra__notify?\n",
			expectDetected: true,
			expectedAgent:  "claude",
			expectedCmd:    "mcp__orkestra__notify",
		},
		{
			name:           "No prompt in output",
			input:          "Regular output without any permission prompt\n",
			expectDetected: false,
		},
		{
			name:           "Incomplete prompt line",
			input:          "Allow claude to execute this Bash command? ls -la",
			expectDetected: true,
			expectedAgent:  "claude",
			expectedCmd:    "ls -la",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspaceID := "test-workspace"
			_ = state.CleanupApprovalState(configDir, workspaceID)
			defer state.CleanupApprovalState(configDir, workspaceID)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			var approvalWg sync.WaitGroup
			var lineBuffer bytes.Buffer

			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("failed to create pipe: %v", err)
			}
			defer r.Close()
			defer w.Close()

			start := time.Now()
			result := processWithPromptDetection(
				ctx,
				[]byte(tt.input),
				&lineBuffer,
				workspaceID,
				configDir,
				5*time.Second,
				w,
				&approvalWg,
			)
			if time.Since(start) > 200*time.Millisecond {
				t.Fatal("processWithPromptDetection blocked on approval request")
			}

			if string(result) != tt.input {
				t.Errorf("expected unchanged output, got %q", result)
			}

			req, err := state.ReadPendingApproval(configDir, workspaceID)
			if err != nil {
				t.Fatalf("failed to read pending approval: %v", err)
			}
			if tt.expectDetected {
				if req == nil {
					t.Fatal("expected pending approval to exist")
				}
				if req.Agent != tt.expectedAgent {
					t.Errorf("agent = %q, want %q", req.Agent, tt.expectedAgent)
				}
				if req.Command != tt.expectedCmd {
					t.Errorf("command = %q, want %q", req.Command, tt.expectedCmd)
				}
			} else if req != nil {
				t.Fatalf("unexpected pending approval: %+v", req)
			}

			cancel()
			approvalWg.Wait()
		})
	}
}

func TestHandleApprovalRequest_AutoReject(t *testing.T) {
	configDir := t.TempDir()

	workspaceID := "test-workspace-timeout"
	agent := "claude"
	command := "git status"
	timeout := 200 * time.Millisecond

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	_ = state.CleanupApprovalState(configDir, workspaceID)
	defer state.CleanupApprovalState(configDir, workspaceID)

	var approvalWg sync.WaitGroup
	done := make(chan struct{})
	go func() {
		handleApprovalRequest(context.Background(), workspaceID, agent, command, configDir, timeout, w, &approvalWg)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("handleApprovalRequest blocked instead of returning immediately")
	}

	buf := make([]byte, 2)
	r.SetReadDeadline(time.Now().Add(timeout + 500*time.Millisecond))
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("failed to read injected response: %v", err)
	}
	if n != 2 || string(buf[:n]) != "n\n" {
		t.Errorf("expected 'n\\n', got %q", buf[:n])
	}
	approvalWg.Wait()
}

func TestHandleApprovalRequest_Approve(t *testing.T) {
	configDir := t.TempDir()

	workspaceID := "test-workspace-approve"
	agent := "claude"
	command := "git status"
	timeout := 5 * time.Second

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	_ = state.CleanupApprovalState(configDir, workspaceID)
	defer state.CleanupApprovalState(configDir, workspaceID)

	var approvalWg sync.WaitGroup
	handleApprovalRequest(context.Background(), workspaceID, agent, command, configDir, timeout, w, &approvalWg)

	req, err := state.ReadPendingApproval(configDir, workspaceID)
	if err != nil {
		t.Fatalf("failed to read pending approval: %v", err)
	}
	if req == nil {
		t.Fatal("expected pending approval to exist")
	}

	resp := state.ApprovalResponse{
		RequestID:   req.ID,
		Approved:    true,
		RespondedAt: time.Now(),
		RespondedBy: "test-user",
	}
	if err := state.WriteApprovalResponse(configDir, workspaceID, resp); err != nil {
		t.Fatalf("failed to write approval response: %v", err)
	}

	buf := make([]byte, 2)
	r.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("failed to read injected response: %v", err)
	}
	if n != 2 || string(buf[:n]) != "y\n" {
		t.Errorf("expected 'y\\n', got %q", buf[:n])
	}
	approvalWg.Wait()
}

func TestHandleApprovalRequest_Reject(t *testing.T) {
	configDir := t.TempDir()

	workspaceID := "test-workspace-reject"
	agent := "claude"
	command := "rm -rf /"
	timeout := 5 * time.Second

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	_ = state.CleanupApprovalState(configDir, workspaceID)
	defer state.CleanupApprovalState(configDir, workspaceID)

	var approvalWg sync.WaitGroup
	handleApprovalRequest(context.Background(), workspaceID, agent, command, configDir, timeout, w, &approvalWg)

	req, err := state.ReadPendingApproval(configDir, workspaceID)
	if err != nil {
		t.Fatalf("failed to read pending approval: %v", err)
	}
	if req == nil {
		t.Fatal("expected pending approval to exist")
	}
	if req.RiskLevel != risk.Dangerous {
		t.Errorf("expected Dangerous risk level, got %v", req.RiskLevel)
	}

	resp := state.ApprovalResponse{
		RequestID:   req.ID,
		Approved:    false,
		RespondedAt: time.Now(),
		RespondedBy: "test-user",
	}
	if err := state.WriteApprovalResponse(configDir, workspaceID, resp); err != nil {
		t.Fatalf("failed to write approval response: %v", err)
	}

	buf := make([]byte, 2)
	r.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("failed to read injected response: %v", err)
	}
	if n != 2 || string(buf[:n]) != "n\n" {
		t.Errorf("expected 'n\\n', got %q", buf[:n])
	}
	approvalWg.Wait()
}

func TestInjectResponse(t *testing.T) {
	tests := []struct {
		name     string
		approved bool
		expected string
	}{
		{
			name:     "Approve",
			approved: true,
			expected: "y\n",
		},
		{
			name:     "Reject",
			approved: false,
			expected: "n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("failed to create pipe: %v", err)
			}
			defer r.Close()
			defer w.Close()

			injectResponse(w, tt.approved)

			buf := make([]byte, 2)
			r.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, err := r.Read(buf)
			if err != nil {
				t.Fatalf("failed to read: %v", err)
			}

			if string(buf[:n]) != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, buf[:n])
			}
		})
	}
}
