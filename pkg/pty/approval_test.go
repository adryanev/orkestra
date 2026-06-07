//go:build !windows

package pty

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/adryanev/orkestra/pkg/risk"
	"github.com/adryanev/orkestra/pkg/state"
)

func TestProcessWithPromptDetection(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectDetected bool
		expectedAgent  string
		expectedCmd    string
	}{
		{
			name:           "Claude bash command prompt",
			input:          "Allow claude to execute this Bash command?\n  git status\n",
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
			var lineBuffer bytes.Buffer

			// Create a test PTY master (just for API compatibility - we won't actually write to it in this test)
			// In a real test, you'd use a pipe or mock
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("failed to create pipe: %v", err)
			}
			defer r.Close()
			defer w.Close()

			// Process the data
			result := processWithPromptDetection(
				[]byte(tt.input),
				&lineBuffer,
				"test-workspace",
				1*time.Second,
				w, // Use write end of pipe as mock master
			)

			// Verify the result is unchanged (we just detect, not modify)
			if string(result) != tt.input {
				t.Errorf("expected unchanged output, got %q", result)
			}

			// Note: In this unit test we can't easily verify the approval workflow was triggered
			// without mocking the state package. That would be better tested in an integration test.
		})
	}
}

func TestHandleApprovalRequest_AutoReject(t *testing.T) {
	// This test verifies that when no response is received, the request times out and auto-rejects

	workspaceID := "test-workspace-timeout"
	agent := "claude"
	command := "git status"
	timeout := 200 * time.Millisecond

	// Create a pipe to capture injected response
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	// Clean up any existing state
	_ = state.CleanupApprovalState(workspaceID)
	defer state.CleanupApprovalState(workspaceID)

	// Call handleApprovalRequest in a goroutine (it will block waiting for response)
	done := make(chan struct{})
	go func() {
		handleApprovalRequest(workspaceID, agent, command, timeout, w)
		close(done)
	}()

	// Wait for timeout + small buffer
	select {
	case <-done:
		// Expected - function should complete after timeout
	case <-time.After(timeout + 500*time.Millisecond):
		t.Fatal("handleApprovalRequest did not complete within expected time")
	}

	// Verify "n\n" was injected (auto-reject on timeout)
	buf := make([]byte, 2)
	r.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("failed to read injected response: %v", err)
	}
	if n != 2 || string(buf[:n]) != "n\n" {
		t.Errorf("expected 'n\\n', got %q", buf[:n])
	}
}

func TestHandleApprovalRequest_Approve(t *testing.T) {
	// This test verifies that when an approval response is written, it's read and injected

	workspaceID := "test-workspace-approve"
	agent := "claude"
	command := "git status"
	timeout := 5 * time.Second

	// Create a pipe to capture injected response
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	// Clean up any existing state
	_ = state.CleanupApprovalState(workspaceID)
	defer state.CleanupApprovalState(workspaceID)

	// Call handleApprovalRequest in a goroutine
	done := make(chan struct{})
	go func() {
		handleApprovalRequest(workspaceID, agent, command, timeout, w)
		close(done)
	}()

	// Wait a moment for the request to be written
	time.Sleep(100 * time.Millisecond)

	// Write an approval response
	resp := state.ApprovalResponse{
		RequestID:   "test-req-id",
		Approved:    true,
		RespondedAt: time.Now(),
		RespondedBy: "test-user",
	}
	if err := state.WriteApprovalResponse(workspaceID, resp); err != nil {
		t.Fatalf("failed to write approval response: %v", err)
	}

	// Wait for the handler to process the response
	select {
	case <-done:
		// Expected
	case <-time.After(2 * time.Second):
		t.Fatal("handleApprovalRequest did not complete within expected time")
	}

	// Verify "y\n" was injected (approval)
	buf := make([]byte, 2)
	r.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("failed to read injected response: %v", err)
	}
	if n != 2 || string(buf[:n]) != "y\n" {
		t.Errorf("expected 'y\\n', got %q", buf[:n])
	}
}

func TestHandleApprovalRequest_Reject(t *testing.T) {
	// This test verifies that when a rejection response is written, it's read and injected

	workspaceID := "test-workspace-reject"
	agent := "claude"
	command := "rm -rf /"
	timeout := 5 * time.Second

	// Create a pipe to capture injected response
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	// Clean up any existing state
	_ = state.CleanupApprovalState(workspaceID)
	defer state.CleanupApprovalState(workspaceID)

	// Call handleApprovalRequest in a goroutine
	done := make(chan struct{})
	go func() {
		handleApprovalRequest(workspaceID, agent, command, timeout, w)
		close(done)
	}()

	// Wait a moment for the request to be written
	time.Sleep(100 * time.Millisecond)

	// Verify the request was written with correct risk level
	req, err := state.ReadPendingApproval(workspaceID)
	if err != nil {
		t.Fatalf("failed to read pending approval: %v", err)
	}
	if req == nil {
		t.Fatal("expected pending approval to exist")
	}
	if req.RiskLevel != risk.Dangerous {
		t.Errorf("expected Dangerous risk level, got %v", req.RiskLevel)
	}

	// Write a rejection response
	resp := state.ApprovalResponse{
		RequestID:   req.ID,
		Approved:    false,
		RespondedAt: time.Now(),
		RespondedBy: "test-user",
	}
	if err := state.WriteApprovalResponse(workspaceID, resp); err != nil {
		t.Fatalf("failed to write approval response: %v", err)
	}

	// Wait for the handler to process the response
	select {
	case <-done:
		// Expected
	case <-time.After(2 * time.Second):
		t.Fatal("handleApprovalRequest did not complete within expected time")
	}

	// Verify "n\n" was injected (rejection)
	buf := make([]byte, 2)
	r.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("failed to read injected response: %v", err)
	}
	if n != 2 || string(buf[:n]) != "n\n" {
		t.Errorf("expected 'n\\n', got %q", buf[:n])
	}
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

			// Inject response
			injectResponse(w, tt.approved)

			// Read back
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
