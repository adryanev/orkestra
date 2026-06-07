package mcp

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPendingPath(t *testing.T) {
	tests := []struct {
		name        string
		workspaceID string
		wantErr     bool
	}{
		{
			name:        "valid workspace ID",
			workspaceID: "ws-123",
			wantErr:     false,
		},
		{
			name:        "reject forward slash",
			workspaceID: "../evil",
			wantErr:     true,
		},
		{
			name:        "reject backslash",
			workspaceID: "..\\evil",
			wantErr:     true,
		},
		{
			name:        "reject dot-dot",
			workspaceID: "test..test",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := pendingPath("/tmp/orkestra", tt.workspaceID)
			if (err != nil) != tt.wantErr {
				t.Errorf("pendingPath() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAnswerPath(t *testing.T) {
	tests := []struct {
		name        string
		workspaceID string
		wantErr     bool
	}{
		{
			name:        "valid workspace ID",
			workspaceID: "ws-456",
			wantErr:     false,
		},
		{
			name:        "reject path traversal",
			workspaceID: "../../etc/passwd",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := answerPath("/tmp/orkestra", tt.workspaceID)
			if (err != nil) != tt.wantErr {
				t.Errorf("answerPath() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestWritePending_HappyPath(t *testing.T) {
	configDir := t.TempDir()
	workspaceID := "test-workspace"

	q := PendingQuestion{
		WorkspaceID: workspaceID,
		Question:    "Should I proceed?",
		Options:     []string{"yes", "no"},
		AskedAt:     time.Now().UTC(),
	}

	// First write should succeed
	if err := WritePending(configDir, workspaceID, q); err != nil {
		t.Fatalf("WritePending() failed: %v", err)
	}

	// Verify file was created
	path := filepath.Join(configDir, "pending", workspaceID+".json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("pending file was not created at %s", path)
	}
}

func TestWritePending_RejectsDuplicate(t *testing.T) {
	configDir := t.TempDir()
	workspaceID := "test-workspace"

	q := PendingQuestion{
		WorkspaceID: workspaceID,
		Question:    "First question",
		AskedAt:     time.Now().UTC(),
	}

	// First write
	if err := WritePending(configDir, workspaceID, q); err != nil {
		t.Fatalf("first WritePending() failed: %v", err)
	}

	// Second write should fail (R4 guard)
	q2 := PendingQuestion{
		WorkspaceID: workspaceID,
		Question:    "Second question",
		AskedAt:     time.Now().UTC(),
	}
	if err := WritePending(configDir, workspaceID, q2); err == nil {
		t.Error("WritePending() should reject duplicate pending file, but got nil error")
	}
}

func TestReadPending_HappyPath(t *testing.T) {
	configDir := t.TempDir()
	workspaceID := "test-workspace"

	q := PendingQuestion{
		WorkspaceID: workspaceID,
		Question:    "Test question?",
		Options:     []string{"a", "b"},
		AskedAt:     time.Now().UTC().Truncate(time.Second), // Truncate for comparison
	}

	// Write
	if err := WritePending(configDir, workspaceID, q); err != nil {
		t.Fatalf("WritePending() failed: %v", err)
	}

	// Read
	got, err := ReadPending(configDir, workspaceID)
	if err != nil {
		t.Fatalf("ReadPending() failed: %v", err)
	}
	if got == nil {
		t.Fatal("ReadPending() returned nil for existing file")
	}

	// Verify fields
	if got.WorkspaceID != q.WorkspaceID {
		t.Errorf("WorkspaceID = %q, want %q", got.WorkspaceID, q.WorkspaceID)
	}
	if got.Question != q.Question {
		t.Errorf("Question = %q, want %q", got.Question, q.Question)
	}
	if len(got.Options) != len(q.Options) {
		t.Errorf("Options length = %d, want %d", len(got.Options), len(q.Options))
	}
	if !got.AskedAt.Equal(q.AskedAt) {
		t.Errorf("AskedAt = %v, want %v", got.AskedAt, q.AskedAt)
	}
}

func TestReadPending_MissingFile(t *testing.T) {
	configDir := t.TempDir()
	workspaceID := "nonexistent-workspace"

	// Read nonexistent file should return (nil, nil)
	got, err := ReadPending(configDir, workspaceID)
	if err != nil {
		t.Errorf("ReadPending() returned error for missing file: %v", err)
	}
	if got != nil {
		t.Errorf("ReadPending() = %v, want nil for missing file", got)
	}
}

func TestDeletePending_HappyPath(t *testing.T) {
	configDir := t.TempDir()
	workspaceID := "test-workspace"

	q := PendingQuestion{
		WorkspaceID: workspaceID,
		Question:    "Delete me",
		AskedAt:     time.Now().UTC(),
	}

	// Write
	if err := WritePending(configDir, workspaceID, q); err != nil {
		t.Fatalf("WritePending() failed: %v", err)
	}

	// Delete
	if err := DeletePending(configDir, workspaceID); err != nil {
		t.Fatalf("DeletePending() failed: %v", err)
	}

	// Verify file is gone
	got, err := ReadPending(configDir, workspaceID)
	if err != nil {
		t.Errorf("ReadPending() after delete failed: %v", err)
	}
	if got != nil {
		t.Errorf("ReadPending() after delete = %v, want nil", got)
	}
}

func TestDeletePending_Idempotent(t *testing.T) {
	configDir := t.TempDir()
	workspaceID := "test-workspace"

	// Delete nonexistent file should not error
	if err := DeletePending(configDir, workspaceID); err != nil {
		t.Errorf("DeletePending() on missing file returned error: %v", err)
	}
}

func TestAppendAnswer_HappyPath(t *testing.T) {
	configDir := t.TempDir()
	workspaceID := "test-workspace"

	r1 := AnswerRecord{
		WorkspaceID: workspaceID,
		Question:    "First question",
		Answer:      "yes",
		AskedAt:     time.Now().UTC().Truncate(time.Second),
		AnsweredAt:  time.Now().UTC().Truncate(time.Second),
	}

	// First append (creates file)
	if err := AppendAnswer(configDir, workspaceID, r1); err != nil {
		t.Fatalf("first AppendAnswer() failed: %v", err)
	}

	r2 := AnswerRecord{
		WorkspaceID: workspaceID,
		Question:    "Second question",
		Answer:      "no",
		AskedAt:     time.Now().UTC().Truncate(time.Second),
		AnsweredAt:  time.Now().UTC().Truncate(time.Second),
	}

	// Second append
	if err := AppendAnswer(configDir, workspaceID, r2); err != nil {
		t.Fatalf("second AppendAnswer() failed: %v", err)
	}

	// Read and verify both records are present
	path := filepath.Join(configDir, "answers", workspaceID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read answer file: %v", err)
	}

	// Should contain both records (basic smoke test — full parsing tested above)
	content := string(data)
	if !contains(content, "First question") || !contains(content, "Second question") {
		t.Errorf("answer file missing expected records: %s", content)
	}
}

func TestAppendAnswer_WithOptions(t *testing.T) {
	configDir := t.TempDir()
	workspaceID := "test-workspace"

	r := AnswerRecord{
		WorkspaceID: workspaceID,
		Question:    "Choose one",
		Options:     []string{"option1", "option2", "option3"},
		Answer:      "option2",
		AskedAt:     time.Now().UTC().Truncate(time.Second),
		AnsweredAt:  time.Now().UTC().Truncate(time.Second),
	}

	if err := AppendAnswer(configDir, workspaceID, r); err != nil {
		t.Fatalf("AppendAnswer() with options failed: %v", err)
	}

	// Verify options are present in file
	path := filepath.Join(configDir, "answers", workspaceID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read answer file: %v", err)
	}

	content := string(data)
	if !contains(content, "option1") || !contains(content, "option2") {
		t.Errorf("answer file missing expected options: %s", content)
	}
}

func TestPendingQuestion_OmitemptyOptions(t *testing.T) {
	configDir := t.TempDir()
	workspaceID := "test-workspace"

	// Question with no options (free-form answer)
	q := PendingQuestion{
		WorkspaceID: workspaceID,
		Question:    "What is your name?",
		// Options omitted
		AskedAt: time.Now().UTC(),
	}

	if err := WritePending(configDir, workspaceID, q); err != nil {
		t.Fatalf("WritePending() without options failed: %v", err)
	}

	// Read back and verify
	got, err := ReadPending(configDir, workspaceID)
	if err != nil {
		t.Fatalf("ReadPending() failed: %v", err)
	}
	if got.Options != nil {
		t.Errorf("Options should be nil for omitted field, got %v", got.Options)
	}
}

func TestWritePending_InvalidWorkspaceID(t *testing.T) {
	configDir := t.TempDir()

	q := PendingQuestion{
		WorkspaceID: "../evil",
		Question:    "Bad question",
		AskedAt:     time.Now().UTC(),
	}

	err := WritePending(configDir, "../evil", q)
	if err == nil {
		t.Error("WritePending() with invalid workspace ID should fail")
	}
}

func TestAppendAnswer_InvalidWorkspaceID(t *testing.T) {
	configDir := t.TempDir()

	r := AnswerRecord{
		WorkspaceID: "../../etc/passwd",
		Question:    "Bad question",
		Answer:      "bad answer",
		AskedAt:     time.Now().UTC(),
		AnsweredAt:  time.Now().UTC(),
	}

	err := AppendAnswer(configDir, "../../etc/passwd", r)
	if err == nil {
		t.Error("AppendAnswer() with invalid workspace ID should fail")
	}
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && containsHelper(s, substr)))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
