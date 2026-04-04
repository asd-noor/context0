package daemon_test

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"context0/internal/daemon"
)

// ── WritePID / RemovePID ──────────────────────────────────────────────────────

// TestWritePIDCreatesFile verifies that WritePID creates a file containing the
// current process PID.
func TestWritePIDCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.pid")

	if err := daemon.WritePID(path); err != nil {
		t.Fatalf("WritePID: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read PID file: %v", err)
	}

	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatalf("parse PID %q: %v", data, err)
	}
	if pid != os.Getpid() {
		t.Errorf("PID mismatch: got %d, want %d", pid, os.Getpid())
	}
}

// TestRemovePIDDeletesFile verifies that RemovePID removes an existing PID
// file.
func TestRemovePIDDeletesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.pid")
	if err := os.WriteFile(path, []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}

	daemon.RemovePID(path)

	if _, err := os.Stat(path); err == nil {
		t.Error("PID file still exists after RemovePID")
	}
}

// TestRemovePIDNonExistentIsNoop verifies that RemovePID on a missing path
// does not panic or return an error.
func TestRemovePIDNonExistentIsNoop(t *testing.T) {
	daemon.RemovePID(filepath.Join(t.TempDir(), "ghost.pid")) // must not panic
}

// ── IsAlive ───────────────────────────────────────────────────────────────────

// TestIsAliveCurrentProcess verifies that IsAlive returns true when the PID
// file contains the current process's PID.
func TestIsAliveCurrentProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "self.pid")
	if err := daemon.WritePID(path); err != nil {
		t.Fatalf("WritePID: %v", err)
	}

	if !daemon.IsAlive(path) {
		t.Error("IsAlive: expected true for current process PID, got false")
	}
}

// TestIsAliveMissingFile verifies that IsAlive returns false when the PID
// file does not exist.
func TestIsAliveMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-such.pid")
	if daemon.IsAlive(path) {
		t.Error("IsAlive: expected false for missing PID file, got true")
	}
}

// TestIsAliveGarbageContent verifies that IsAlive returns false when the PID
// file contains non-numeric data.
func TestIsAliveGarbageContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.pid")
	if err := os.WriteFile(path, []byte("not-a-pid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if daemon.IsAlive(path) {
		t.Error("IsAlive: expected false for garbage PID content, got true")
	}
}

// TestIsAliveZeroPID verifies that IsAlive returns false when the PID file
// contains zero (an invalid PID).
func TestIsAliveZeroPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero.pid")
	if err := os.WriteFile(path, []byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}
	if daemon.IsAlive(path) {
		t.Error("IsAlive: expected false for PID=0, got true")
	}
}

// TestIsAliveDeadProcess verifies that IsAlive returns false for a PID that
// is not a running process. We use a very large PID that is almost certainly
// unoccupied; on Linux the max default is 32768 and on macOS 99998.
func TestIsAliveDeadProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dead.pid")
	// PID 2147483647 (max int32) will not exist on any real system.
	if err := os.WriteFile(path, []byte("2147483647"), 0o644); err != nil {
		t.Fatal(err)
	}
	if daemon.IsAlive(path) {
		t.Error("IsAlive: expected false for non-existent PID, got true")
	}
}
