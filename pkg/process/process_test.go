package process

import (
	"os/exec"
	"testing"
	"time"
)

// startGroupLeader starts a shell running shellCmd as its own process-group
// leader and returns the PID (which equals the PGID under Setpgid).
func startGroupLeader(t *testing.T, shell, shellCmd string) int {
	t.Helper()
	cmd := exec.Command(shell, "-c", shellCmd)
	cmd.SysProcAttr = SysProcAttr()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	// Reap in the background so the kernel does not keep a zombie that Alive
	// would still report as existing.
	go func() { _ = cmd.Wait() }()
	return pid
}

// waitGone polls until pid is no longer alive or the deadline elapses.
func waitGone(pid int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if !Alive(pid) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return !Alive(pid)
}

func TestAliveFalseForBogusPID(t *testing.T) {
	if Alive(0) || Alive(-1) {
		t.Error("Alive should be false for non-positive pids")
	}
}

func TestPGIDRejectsInvalidPID(t *testing.T) {
	if _, err := PGID(0); err == nil {
		t.Error("PGID(0) should return error")
	}
	if _, err := PGID(-1); err == nil {
		t.Error("PGID(-1) should return error")
	}
}

// TestTerminateGroupHappyPath: a long-running group leader is terminated by a
// separate code path (mirroring run vs stop) and is gone afterward.
func TestTerminateGroupHappyPath(t *testing.T) {
	pid := startGroupLeader(t, "sh", "sleep 30")
	if !Alive(pid) {
		t.Fatal("process should be alive right after start")
	}
	if err := TerminateGroup(pid, DefaultGrace); err != nil {
		t.Fatalf("TerminateGroup: %v", err)
	}
	if !waitGone(pid, 2*time.Second) {
		t.Error("process group should be gone after TerminateGroup")
	}
}

func TestIdentityMatchesProcessGroupLeader(t *testing.T) {
	pid := startGroupLeader(t, "sh", "sleep 30")
	defer func() { _ = TerminateGroup(pid, DefaultGrace) }()

	startedAt, err := StartedAt(pid)
	if err != nil {
		t.Fatalf("StartedAt: %v", err)
	}
	if !IdentityMatches(pid, pid, startedAt) {
		t.Fatal("expected identity to match the live process group leader")
	}
	if IdentityMatches(pid, pid+1, startedAt) {
		t.Fatal("expected mismatched pgid to fail identity validation")
	}
	if IdentityMatches(pid, pid, startedAt+1) {
		t.Fatal("expected mismatched start token to fail identity validation")
	}
}

// TestTerminateGroupGraceEscalation: a process that ignores SIGTERM is still
// killed by the follow-up SIGKILL after the grace period.
func TestTerminateGroupGraceEscalation(t *testing.T) {
	// A SIGTERM-ignoring leader is needed to force the SIGKILL escalation.
	// bash honors `trap '' TERM` reliably; the platform /bin/sh on macOS does
	// not, so the test targets bash and skips where it is unavailable.
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available; skipping SIGKILL-escalation test")
	}
	// The while-loop keeps bash resident (a bare trailing `sleep` would be
	// exec-optimized away, dropping the trap), so the leader genuinely
	// survives SIGTERM and forces the SIGKILL escalation.
	pid := startGroupLeader(t, bash, "trap '' TERM; while :; do sleep 1; done")
	if !Alive(pid) {
		t.Fatal("process should be alive right after start")
	}
	// Give bash time to install the trap before the first SIGTERM, otherwise
	// the signal races the `trap` builtin and the default disposition kills it.
	time.Sleep(200 * time.Millisecond)
	start := time.Now()
	if err := TerminateGroup(pid, 300*time.Millisecond); err != nil {
		t.Fatalf("TerminateGroup: %v", err)
	}
	if time.Since(start) < 250*time.Millisecond {
		t.Error("expected TerminateGroup to wait the grace period before SIGKILL")
	}
	if !waitGone(pid, 2*time.Second) {
		t.Error("SIGTERM-ignoring process should be killed by SIGKILL")
	}
}

// TestTerminateGroupAlreadyGone: terminating an exited process group is a
// no-op success (idempotent stop).
func TestTerminateGroupAlreadyGone(t *testing.T) {
	pid := startGroupLeader(t, "sh", "true")
	if !waitGone(pid, 2*time.Second) {
		t.Fatal("short process should have exited")
	}
	if err := TerminateGroup(pid, DefaultGrace); err != nil {
		t.Errorf("terminating an already-gone group should succeed, got %v", err)
	}
}

func TestTerminateGroupZeroPGID(t *testing.T) {
	if err := TerminateGroup(0, DefaultGrace); err != nil {
		t.Errorf("TerminateGroup(0) should be a no-op, got %v", err)
	}
}
