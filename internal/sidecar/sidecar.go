// Package sidecar manages the Python sidecar lifecycle and provides a
// lightweight JSON client for communicating with it over a Unix Domain Socket.
//
// Liveness is determined socket-first (connect attempt to channel.sock) rather
// than PID-first, because PID files can go stale after a crash.  The PID file
// is still maintained by the Python process and is used only by [Stop].
package sidecar

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ---------------------------------------------------------------------------
// Sentinel errors
// ---------------------------------------------------------------------------

var (
	// ErrAlreadyRunning is returned by [Start] when the sidecar socket is
	// already accepting connections.
	ErrAlreadyRunning = errors.New("sidecar is already running")

	// ErrNotRunning is returned by [Stop] when no live sidecar can be found.
	ErrNotRunning = errors.New("sidecar is not running")
)

// ---------------------------------------------------------------------------
// Paths  (overridable via environment variables for testing)
// ---------------------------------------------------------------------------

// SocketPath returns the Unix Domain Socket path for the sidecar.
// Override with $CTX0_SOCKET.
func SocketPath() string {
	if v := os.Getenv("CTX0_SOCKET"); v != "" {
		return v
	}
	return filepath.Join(homeDir(), ".context0", "channel.sock")
}

// PIDPath returns the path to the sidecar PID file.
// Override with $CTX0_SIDECAR_PID.
func PIDPath() string {
	if v := os.Getenv("CTX0_SIDECAR_PID"); v != "" {
		return v
	}
	return filepath.Join(homeDir(), ".context0", "sidecar.pid")
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

// ---------------------------------------------------------------------------
// Liveness
// ---------------------------------------------------------------------------

// IsRunning reports whether the sidecar is accepting connections on its socket.
// This is the canonical liveness check — a socket-first approach that detects
// stale PID files left by crashed processes.
func IsRunning() bool {
	conn, err := net.DialTimeout("unix", SocketPath(), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

// Start spawns the Python sidecar as a detached background process via
// `uv run sidecar/main.py`.  The sidecar executable is located relative to
// the running context0 binary (sidecar/main.py lives alongside it).
//
// Returns [ErrAlreadyRunning] if the socket is already live.
func Start() error {
	if IsRunning() {
		return ErrAlreadyRunning
	}

	mainPy, projectRoot, err := findSidecarMain()
	if err != nil {
		return err
	}

	cmd := exec.Command("uv", "run", mainPy)
	cmd.Dir = projectRoot
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	// Detach from the terminal so the child outlives the caller.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("sidecar: spawn failed (is uv installed?): %w", err)
	}
	// Release the child without waiting — it runs independently.
	go func() { _ = cmd.Wait() }() //nolint:errcheck
	return nil
}

// Stop sends SIGTERM to the sidecar process identified by the PID file.
// Returns [ErrNotRunning] if the PID file is absent or the process is gone.
func Stop() error {
	data, err := os.ReadFile(PIDPath())
	if err != nil {
		return ErrNotRunning
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return ErrNotRunning
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return ErrNotRunning
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return ErrNotRunning
	}
	return nil
}

// findSidecarMain locates sidecar/main.py relative to the running binary.
// Returns (mainPyPath, projectRoot, error).
func findSidecarMain() (string, string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", "", fmt.Errorf("sidecar: cannot determine executable path: %w", err)
	}
	// Resolve symlinks to get the real on-disk location.
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", "", fmt.Errorf("sidecar: eval symlinks: %w", err)
	}

	projectRoot := filepath.Dir(exe)
	candidate := filepath.Join(projectRoot, "sidecar", "main.py")
	if _, statErr := os.Stat(candidate); statErr == nil {
		return candidate, projectRoot, nil
	}
	return "", "", fmt.Errorf(
		"sidecar: cannot find sidecar/main.py alongside binary at %s — "+
			"ensure the sidecar directory is deployed with the binary",
		exe,
	)
}

// ---------------------------------------------------------------------------
// JSON client
// ---------------------------------------------------------------------------

// Request is the envelope sent to the sidecar over the UDS.
type Request map[string]any

// Response is the envelope received from the sidecar.
type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Remaining fields are command-specific and decoded by callers.
	Extra map[string]json.RawMessage `json:"-"`
}

// Send opens a fresh connection to the sidecar, writes req as a single JSON
// line, reads the response line, and returns it.  The connection is closed
// before returning.
//
// Callers that need command-specific response fields should decode the raw
// JSON themselves; [SendRaw] is provided for that purpose.
func Send(req Request) (*Response, error) {
	raw, err := SendRaw(req)
	if err != nil {
		return nil, err
	}
	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("sidecar: decode response: %w", err)
	}
	return &resp, nil
}

// SendRaw sends req and returns the raw JSON response bytes.
func SendRaw(req Request) ([]byte, error) {
	conn, err := net.DialTimeout("unix", SocketPath(), 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("sidecar: connect: %w (is the sidecar running? use `context0 --daemon`)", err)
	}
	defer conn.Close() //nolint:errcheck

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("sidecar: encode request: %w", err)
	}
	payload = append(payload, '\n')

	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return nil, fmt.Errorf("sidecar: set write deadline: %w", err)
	}
	if _, err := conn.Write(payload); err != nil {
		return nil, fmt.Errorf("sidecar: write: %w", err)
	}

	// Read until the connection is closed (one line response).
	if err := conn.SetReadDeadline(time.Now().Add(120 * time.Second)); err != nil {
		return nil, fmt.Errorf("sidecar: set read deadline: %w", err)
	}
	var buf []byte
	tmp := make([]byte, 4096)
	for {
		n, readErr := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if readErr != nil {
			break
		}
	}
	if len(buf) == 0 {
		return nil, fmt.Errorf("sidecar: empty response")
	}
	return buf, nil
}
