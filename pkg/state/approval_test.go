package state

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/adryanev/orkestra/pkg/risk"
	"github.com/google/uuid"
)

func TestNewApprovalRequest(t *testing.T) {
	workspaceID := "test-workspace"
	agent := "claude"
	command := "rm -rf /tmp/test"
	riskLevel := risk.Dangerous
	timeout := 5 * time.Minute

	req := NewApprovalRequest(workspaceID, agent, command, riskLevel, timeout)

	if req.ID == "" {
		t.Error("Expected non-empty ID")
	}
	if _, err := uuid.Parse(req.ID); err != nil {
		t.Errorf("Expected valid UUID, got: %s", req.ID)
	}
	if req.WorkspaceID != workspaceID {
		t.Errorf("Expected workspace ID %s, got %s", workspaceID, req.WorkspaceID)
	}
	if req.Agent != agent {
		t.Errorf("Expected agent %s, got %s", agent, req.Agent)
	}
	if req.Command != command {
		t.Errorf("Expected command %s, got %s", command, req.Command)
	}
	if req.RiskLevel != riskLevel {
		t.Errorf("Expected risk level %d, got %d", riskLevel, req.RiskLevel)
	}
	if req.RequestedAt.IsZero() {
		t.Error("Expected non-zero RequestedAt")
	}
	if req.TimeoutAt.IsZero() {
		t.Error("Expected non-zero TimeoutAt")
	}
	if req.TimeoutAt.Before(req.RequestedAt) {
		t.Error("Expected TimeoutAt to be after RequestedAt")
	}
}

func TestWriteReadPendingApproval(t *testing.T) {
	workspaceID := "test-" + uuid.New().String()
	defer cleanupTestApproval(t, workspaceID)

	req := NewApprovalRequest(workspaceID, "claude", "git push", risk.Moderate, 5*time.Minute)

	// Write request
	if err := WritePendingApproval(workspaceID, req); err != nil {
		t.Fatalf("WritePendingApproval failed: %v", err)
	}

	// Read request
	read, err := ReadPendingApproval(workspaceID)
	if err != nil {
		t.Fatalf("ReadPendingApproval failed: %v", err)
	}

	if read == nil {
		t.Fatal("Expected non-nil request")
	}

	// Compare fields
	if read.ID != req.ID {
		t.Errorf("ID mismatch: expected %s, got %s", req.ID, read.ID)
	}
	if read.WorkspaceID != req.WorkspaceID {
		t.Errorf("WorkspaceID mismatch: expected %s, got %s", req.WorkspaceID, read.WorkspaceID)
	}
	if read.Agent != req.Agent {
		t.Errorf("Agent mismatch: expected %s, got %s", req.Agent, read.Agent)
	}
	if read.Command != req.Command {
		t.Errorf("Command mismatch: expected %s, got %s", req.Command, read.Command)
	}
	if read.RiskLevel != req.RiskLevel {
		t.Errorf("RiskLevel mismatch: expected %d, got %d", req.RiskLevel, read.RiskLevel)
	}
	// Time comparison with tolerance for JSON marshaling precision
	if !read.RequestedAt.Truncate(time.Second).Equal(req.RequestedAt.Truncate(time.Second)) {
		t.Errorf("RequestedAt mismatch: expected %v, got %v", req.RequestedAt, read.RequestedAt)
	}
	if !read.TimeoutAt.Truncate(time.Second).Equal(req.TimeoutAt.Truncate(time.Second)) {
		t.Errorf("TimeoutAt mismatch: expected %v, got %v", req.TimeoutAt, read.TimeoutAt)
	}
}

func TestWriteReadApprovalResponse(t *testing.T) {
	workspaceID := "test-" + uuid.New().String()
	defer cleanupTestApproval(t, workspaceID)

	resp := ApprovalResponse{
		RequestID:   uuid.New().String(),
		Approved:    true,
		RespondedAt: time.Now(),
		RespondedBy: "test-user",
	}

	// Write response
	if err := WriteApprovalResponse(workspaceID, resp); err != nil {
		t.Fatalf("WriteApprovalResponse failed: %v", err)
	}

	// Read response
	read, err := ReadApprovalResponse(workspaceID)
	if err != nil {
		t.Fatalf("ReadApprovalResponse failed: %v", err)
	}

	if read == nil {
		t.Fatal("Expected non-nil response")
	}

	// Compare fields
	if read.RequestID != resp.RequestID {
		t.Errorf("RequestID mismatch: expected %s, got %s", resp.RequestID, read.RequestID)
	}
	if read.Approved != resp.Approved {
		t.Errorf("Approved mismatch: expected %v, got %v", resp.Approved, read.Approved)
	}
	if !read.RespondedAt.Truncate(time.Second).Equal(resp.RespondedAt.Truncate(time.Second)) {
		t.Errorf("RespondedAt mismatch: expected %v, got %v", resp.RespondedAt, read.RespondedAt)
	}
	if read.RespondedBy != resp.RespondedBy {
		t.Errorf("RespondedBy mismatch: expected %s, got %s", resp.RespondedBy, read.RespondedBy)
	}
}

func TestReadPendingApprovalMissing(t *testing.T) {
	workspaceID := "nonexistent-" + uuid.New().String()

	req, err := ReadPendingApproval(workspaceID)
	if err != nil {
		t.Fatalf("ReadPendingApproval should not error on missing file: %v", err)
	}
	if req != nil {
		t.Error("Expected nil request for missing file")
	}
}

func TestReadApprovalResponseMissing(t *testing.T) {
	workspaceID := "nonexistent-" + uuid.New().String()

	resp, err := ReadApprovalResponse(workspaceID)
	if err != nil {
		t.Fatalf("ReadApprovalResponse should not error on missing file: %v", err)
	}
	if resp != nil {
		t.Error("Expected nil response for missing file")
	}
}

func TestCleanupApprovalState(t *testing.T) {
	workspaceID := "test-" + uuid.New().String()
	defer cleanupTestApproval(t, workspaceID)

	// Write both files
	req := NewApprovalRequest(workspaceID, "claude", "ls", risk.Safe, 5*time.Minute)
	if err := WritePendingApproval(workspaceID, req); err != nil {
		t.Fatalf("WritePendingApproval failed: %v", err)
	}

	resp := ApprovalResponse{
		RequestID:   req.ID,
		Approved:    true,
		RespondedAt: time.Now(),
		RespondedBy: "test-user",
	}
	if err := WriteApprovalResponse(workspaceID, resp); err != nil {
		t.Fatalf("WriteApprovalResponse failed: %v", err)
	}

	// Verify files exist
	pendingFile, _ := pendingPath(workspaceID)
	responseFile, _ := responsePath(workspaceID)
	if _, err := os.Stat(pendingFile); os.IsNotExist(err) {
		t.Error("Pending file should exist before cleanup")
	}
	if _, err := os.Stat(responseFile); os.IsNotExist(err) {
		t.Error("Response file should exist before cleanup")
	}

	// Cleanup
	if err := CleanupApprovalState(workspaceID); err != nil {
		t.Fatalf("CleanupApprovalState failed: %v", err)
	}

	// Verify files are removed
	if _, err := os.Stat(pendingFile); !os.IsNotExist(err) {
		t.Error("Pending file should be removed after cleanup")
	}
	if _, err := os.Stat(responseFile); !os.IsNotExist(err) {
		t.Error("Response file should be removed after cleanup")
	}
}

func TestCleanupApprovalStateMissingFiles(t *testing.T) {
	workspaceID := "nonexistent-" + uuid.New().String()

	// Cleanup should not error on missing files
	if err := CleanupApprovalState(workspaceID); err != nil {
		t.Fatalf("CleanupApprovalState should not error on missing files: %v", err)
	}
}

func TestConcurrentWrites(t *testing.T) {
	workspaceID := "test-concurrent-" + uuid.New().String()
	defer cleanupTestApproval(t, workspaceID)

	const numGoroutines = 10
	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2) // Both pending and response writes

	errChan := make(chan error, numGoroutines*2)

	// Concurrent pending writes
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			req := NewApprovalRequest(workspaceID, "claude", "test-command", risk.Safe, 5*time.Minute)
			if err := WritePendingApproval(workspaceID, req); err != nil {
				errChan <- err
			}
		}(i)
	}

	// Concurrent response writes
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			resp := ApprovalResponse{
				RequestID:   uuid.New().String(),
				Approved:    idx%2 == 0,
				RespondedAt: time.Now(),
				RespondedBy: "test-user",
			}
			if err := WriteApprovalResponse(workspaceID, resp); err != nil {
				errChan <- err
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Errorf("Concurrent write error: %v", err)
	}

	// Verify we can read both files
	req, err := ReadPendingApproval(workspaceID)
	if err != nil {
		t.Fatalf("ReadPendingApproval failed after concurrent writes: %v", err)
	}
	if req == nil {
		t.Error("Expected non-nil request after concurrent writes")
	}

	resp, err := ReadApprovalResponse(workspaceID)
	if err != nil {
		t.Fatalf("ReadApprovalResponse failed after concurrent writes: %v", err)
	}
	if resp == nil {
		t.Error("Expected non-nil response after concurrent writes")
	}
}

func TestApprovalDirCreation(t *testing.T) {
	// Get approval dir
	dir, err := approvalDir()
	if err != nil {
		t.Fatalf("approvalDir failed: %v", err)
	}

	// Write a test approval (this should create the directory)
	workspaceID := "test-dir-" + uuid.New().String()
	defer cleanupTestApproval(t, workspaceID)

	req := NewApprovalRequest(workspaceID, "claude", "test", risk.Safe, 5*time.Minute)
	if err := WritePendingApproval(workspaceID, req); err != nil {
		t.Fatalf("WritePendingApproval failed: %v", err)
	}

	// Verify directory exists
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Errorf("Approval directory should exist: %s", dir)
	}
}

// cleanupTestApproval removes test approval files and lock file.
func cleanupTestApproval(t *testing.T, workspaceID string) {
	t.Helper()

	// Cleanup approval state
	_ = CleanupApprovalState(workspaceID)

	// Also remove lock file
	lockFile, err := lockPath(workspaceID)
	if err != nil {
		return
	}
	_ = os.Remove(lockFile)

	// Try to remove the approval directory if empty
	dir, err := approvalDir()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		_ = os.Remove(dir)
	}
}

func TestApprovalPaths(t *testing.T) {
	workspaceID := "test-workspace"

	pending, err := pendingPath(workspaceID)
	if err != nil {
		t.Fatalf("pendingPath failed: %v", err)
	}

	response, err := responsePath(workspaceID)
	if err != nil {
		t.Fatalf("responsePath failed: %v", err)
	}

	lock, err := lockPath(workspaceID)
	if err != nil {
		t.Fatalf("lockPath failed: %v", err)
	}

	home, _ := os.UserHomeDir()
	expectedDir := filepath.Join(home, ".orkestra", "approvals")

	expectedPending := filepath.Join(expectedDir, workspaceID+"_pending.json")
	expectedResponse := filepath.Join(expectedDir, workspaceID+"_response.json")
	expectedLock := filepath.Join(expectedDir, workspaceID+".lock")

	if pending != expectedPending {
		t.Errorf("pendingPath mismatch: expected %s, got %s", expectedPending, pending)
	}
	if response != expectedResponse {
		t.Errorf("responsePath mismatch: expected %s, got %s", expectedResponse, response)
	}
	if lock != expectedLock {
		t.Errorf("lockPath mismatch: expected %s, got %s", expectedLock, lock)
	}
}

// TestConcurrentReadWrite tests concurrent reads and writes to ensure
// readers always get valid data (never partial writes).
func TestConcurrentReadWrite(t *testing.T) {
	workspaceID := "test-concurrent-rw-" + uuid.New().String()
	defer cleanupTestApproval(t, workspaceID)

	const numReaders = 20
	const numWriters = 10
	const duration = 2 * time.Second

	done := make(chan struct{})
	errChan := make(chan error, numReaders+numWriters)
	var wg sync.WaitGroup

	// Start writers that continuously update the approval request
	wg.Add(numWriters)
	for i := 0; i < numWriters; i++ {
		go func(idx int) {
			defer wg.Done()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()

			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					req := NewApprovalRequest(
						workspaceID,
						"claude",
						"test-command-"+uuid.New().String(),
						risk.Safe,
						5*time.Minute,
					)
					if err := WritePendingApproval(workspaceID, req); err != nil {
						errChan <- err
						return
					}
				}
			}
		}(i)
	}

	// Start readers that continuously read and validate
	wg.Add(numReaders)
	for i := 0; i < numReaders; i++ {
		go func(idx int) {
			defer wg.Done()
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()

			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					req, err := ReadPendingApproval(workspaceID)
					if err != nil {
						errChan <- err
						return
					}
					// Validate data consistency if we got a request
					if req != nil {
						// Check that all fields are consistent
						if req.ID == "" {
							errChan <- &consistencyError{"empty ID in concurrent read"}
							return
						}
						if req.WorkspaceID != workspaceID {
							errChan <- &consistencyError{"workspace ID mismatch in concurrent read"}
							return
						}
						if req.Agent != "claude" {
							errChan <- &consistencyError{"agent mismatch in concurrent read"}
							return
						}
						if req.RiskLevel != risk.Safe {
							errChan <- &consistencyError{"risk level mismatch in concurrent read"}
							return
						}
						// Validate UUID format
						if _, err := uuid.Parse(req.ID); err != nil {
							errChan <- &consistencyError{"invalid UUID in concurrent read"}
							return
						}
					}
				}
			}
		}(i)
	}

	// Let it run for a bit
	time.Sleep(duration)
	close(done)
	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Errorf("Concurrent read/write error: %v", err)
	}
}

// TestConcurrentCleanupRace tests that cleanup doesn't race with reads/writes.
func TestConcurrentCleanupRace(t *testing.T) {
	workspaceID := "test-cleanup-race-" + uuid.New().String()
	defer cleanupTestApproval(t, workspaceID)

	const numOps = 50
	var wg sync.WaitGroup
	errChan := make(chan error, numOps*3)

	// Write initial data
	req := NewApprovalRequest(workspaceID, "claude", "test", risk.Safe, 5*time.Minute)
	if err := WritePendingApproval(workspaceID, req); err != nil {
		t.Fatalf("Initial write failed: %v", err)
	}

	resp := ApprovalResponse{
		RequestID:   req.ID,
		Approved:    true,
		RespondedAt: time.Now(),
		RespondedBy: "test-user",
	}
	if err := WriteApprovalResponse(workspaceID, resp); err != nil {
		t.Fatalf("Initial response write failed: %v", err)
	}

	// Run concurrent operations: reads, writes, and cleanups
	wg.Add(numOps * 3)

	// Readers
	for i := 0; i < numOps; i++ {
		go func() {
			defer wg.Done()
			_, err := ReadPendingApproval(workspaceID)
			if err != nil {
				errChan <- err
			}
		}()
	}

	// Writers
	for i := 0; i < numOps; i++ {
		go func() {
			defer wg.Done()
			req := NewApprovalRequest(workspaceID, "claude", "test", risk.Safe, 5*time.Minute)
			if err := WritePendingApproval(workspaceID, req); err != nil {
				errChan <- err
			}
		}()
	}

	// Cleanups
	for i := 0; i < numOps; i++ {
		go func() {
			defer wg.Done()
			if err := CleanupApprovalState(workspaceID); err != nil {
				errChan <- err
			}
		}()
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Errorf("Concurrent cleanup race error: %v", err)
	}
}

// TestDataIntegrityUnderConcurrency verifies that the last write wins
// and data is not corrupted under concurrent writes.
func TestDataIntegrityUnderConcurrency(t *testing.T) {
	workspaceID := "test-integrity-" + uuid.New().String()
	defer cleanupTestApproval(t, workspaceID)

	const numWriters = 100
	var wg sync.WaitGroup
	wg.Add(numWriters)

	// Track all written request IDs
	writtenIDs := make(chan string, numWriters)

	// Concurrent writes with unique IDs
	for i := 0; i < numWriters; i++ {
		go func(idx int) {
			defer wg.Done()
			req := NewApprovalRequest(
				workspaceID,
				"claude",
				"command-"+uuid.New().String(),
				risk.Safe,
				5*time.Minute,
			)
			writtenIDs <- req.ID
			if err := WritePendingApproval(workspaceID, req); err != nil {
				t.Errorf("Write failed: %v", err)
			}
		}(i)
	}

	wg.Wait()
	close(writtenIDs)

	// Collect all written IDs
	idMap := make(map[string]bool)
	for id := range writtenIDs {
		idMap[id] = true
	}

	// Read final state
	finalReq, err := ReadPendingApproval(workspaceID)
	if err != nil {
		t.Fatalf("Final read failed: %v", err)
	}
	if finalReq == nil {
		t.Fatal("Expected non-nil final request")
	}

	// Verify the final request has one of the written IDs
	if !idMap[finalReq.ID] {
		t.Errorf("Final request ID %s not in written IDs", finalReq.ID)
	}

	// Verify data integrity
	if finalReq.WorkspaceID != workspaceID {
		t.Errorf("Workspace ID corrupted: got %s, want %s", finalReq.WorkspaceID, workspaceID)
	}
	if finalReq.Agent != "claude" {
		t.Errorf("Agent corrupted: got %s, want claude", finalReq.Agent)
	}
	if finalReq.RiskLevel != risk.Safe {
		t.Errorf("Risk level corrupted: got %d, want %d", finalReq.RiskLevel, risk.Safe)
	}

	// Validate UUID format
	if _, err := uuid.Parse(finalReq.ID); err != nil {
		t.Errorf("Final ID is not a valid UUID: %v", err)
	}
}

// TestSeparateWorkspaceIsolation ensures that locks for different workspaces
// don't interfere with each other.
func TestSeparateWorkspaceIsolation(t *testing.T) {
	workspace1 := "test-isolation-1-" + uuid.New().String()
	workspace2 := "test-isolation-2-" + uuid.New().String()
	defer cleanupTestApproval(t, workspace1)
	defer cleanupTestApproval(t, workspace2)

	const numOps = 50
	var wg sync.WaitGroup
	wg.Add(numOps * 4) // 2 workspaces × 2 operations each

	errChan := make(chan error, numOps*4)

	// Concurrent writes to workspace 1
	for i := 0; i < numOps; i++ {
		go func() {
			defer wg.Done()
			req := NewApprovalRequest(workspace1, "claude", "test1", risk.Safe, 5*time.Minute)
			if err := WritePendingApproval(workspace1, req); err != nil {
				errChan <- err
			}
		}()
	}

	// Concurrent reads from workspace 1
	for i := 0; i < numOps; i++ {
		go func() {
			defer wg.Done()
			_, err := ReadPendingApproval(workspace1)
			if err != nil {
				errChan <- err
			}
		}()
	}

	// Concurrent writes to workspace 2
	for i := 0; i < numOps; i++ {
		go func() {
			defer wg.Done()
			req := NewApprovalRequest(workspace2, "codex", "test2", risk.Moderate, 5*time.Minute)
			if err := WritePendingApproval(workspace2, req); err != nil {
				errChan <- err
			}
		}()
	}

	// Concurrent reads from workspace 2
	for i := 0; i < numOps; i++ {
		go func() {
			defer wg.Done()
			_, err := ReadPendingApproval(workspace2)
			if err != nil {
				errChan <- err
			}
		}()
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Errorf("Workspace isolation error: %v", err)
	}

	// Verify final state of both workspaces
	req1, err := ReadPendingApproval(workspace1)
	if err != nil {
		t.Fatalf("Read workspace1 failed: %v", err)
	}
	if req1 == nil {
		t.Error("Expected non-nil request for workspace1")
	} else {
		if req1.WorkspaceID != workspace1 {
			t.Errorf("Workspace1 ID mismatch: got %s", req1.WorkspaceID)
		}
		if req1.Agent != "claude" {
			t.Errorf("Workspace1 agent mismatch: got %s", req1.Agent)
		}
	}

	req2, err := ReadPendingApproval(workspace2)
	if err != nil {
		t.Fatalf("Read workspace2 failed: %v", err)
	}
	if req2 == nil {
		t.Error("Expected non-nil request for workspace2")
	} else {
		if req2.WorkspaceID != workspace2 {
			t.Errorf("Workspace2 ID mismatch: got %s", req2.WorkspaceID)
		}
		if req2.Agent != "codex" {
			t.Errorf("Workspace2 agent mismatch: got %s", req2.Agent)
		}
	}
}

// consistencyError represents a data consistency violation.
type consistencyError struct {
	msg string
}

func (e *consistencyError) Error() string {
	return "consistency error: " + e.msg
}
