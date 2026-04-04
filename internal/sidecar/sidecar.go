// Package sidecar manages the Python sidecar lifecycle and provides a
// lightweight JSON client for communicating with it over a Unix Domain Socket.
//
// Liveness is determined socket-first (connect attempt to channel.sock) rather
// than PID-first, because PID files can go stale after a crash.  The PID file
// is still maintained by the Python process and is used only by [Stop].
package sidecar

import (
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
// Embedded filesystem
// ---------------------------------------------------------------------------

// embeddedFS is registered by main.go via [SetFS] before any [Start] call.
// When set, the sidecar source tree is extracted from the binary rather than
// looked up alongside the executable on disk.
var embeddedFS *embed.FS

// SetFS registers the embedded sidecar filesystem.  Call this from main()
// before any other sidecar function.  Passing nil disables extraction and
// falls back to the on-disk sibling-binary lookup (useful in tests).
func SetFS(f *embed.FS) { embeddedFS = f }

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
// `uv run sidecar/main.py`.  The source tree is extracted from the embedded
// filesystem (if registered via [SetFS]) to ~/.context0/sidecar-src/, or
// located alongside the running binary as a fallback.
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

// findSidecarMain returns (mainPyPath, projectRoot) by trying two strategies
// in order:
//
//  1. Embedded FS — extract to ~/.context0/sidecar-src/ (skipped if already
//     up-to-date via content hash), then return paths inside that directory.
//  2. On-disk sibling — look for sidecar/main.py alongside the running binary
//     (development / non-embedded builds).
func findSidecarMain() (string, string, error) {
	if embeddedFS != nil {
		projectRoot, err := extractOnce(embeddedFS)
		if err != nil {
			return "", "", fmt.Errorf("sidecar: extract embedded sources: %w", err)
		}
		return filepath.Join(projectRoot, "sidecar", "main.py"), projectRoot, nil
	}

	// Fallback: look alongside the running binary.
	exe, err := os.Executable()
	if err != nil {
		return "", "", fmt.Errorf("sidecar: cannot determine executable path: %w", err)
	}
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
// Embedded source extraction
// ---------------------------------------------------------------------------

// extractDest is the directory where the embedded sidecar source is unpacked.
func extractDest() string {
	return filepath.Join(homeDir(), ".context0", "sidecar-src")
}

// hashFile is the sentinel written inside extractDest after a successful
// extraction.  Its content is the hex SHA-256 of all embedded file paths and
// their contents.  If it matches on a subsequent run, extraction is skipped.
const hashFile = ".ctx0-hash"

// extractOnce unpacks fsys into extractDest() only when the content hash has
// changed since the last extraction.  Returns the destination directory path.
func extractOnce(fsys *embed.FS) (string, error) {
	dest := extractDest()

	hash, err := fsysHash(fsys)
	if err != nil {
		return "", err
	}

	// Fast path: already up-to-date.
	if existing, readErr := os.ReadFile(filepath.Join(dest, hashFile)); readErr == nil {
		if strings.TrimSpace(string(existing)) == hash {
			return dest, nil
		}
	}

	// Slow path: extract every file from the embedded FS.
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dest, err)
	}

	err = fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		target := filepath.Join(dest, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, readErr := fsys.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read embedded %s: %w", path, readErr)
		}
		if mkErr := os.MkdirAll(filepath.Dir(target), 0o755); mkErr != nil {
			return mkErr
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		return "", fmt.Errorf("extract: %w", err)
	}

	// Record the hash so subsequent starts skip extraction.
	_ = os.WriteFile(filepath.Join(dest, hashFile), []byte(hash), 0o644)
	return dest, nil
}

// fsysHash computes a stable SHA-256 over all file paths and their contents
// in fsys (in WalkDir order, which is lexicographic).
func fsysHash(fsys *embed.FS) (string, error) {
	h := sha256.New()
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		data, err := fsys.ReadFile(path)
		if err != nil {
			return err
		}
		// Include the path so renames are detected, not just content changes.
		fmt.Fprintf(h, "%s\x00", path)
		h.Write(data)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("sidecar: hash embedded fs: %w", err)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
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
		return nil, fmt.Errorf("sidecar: connect: %w (is the sidecar running? use `context0 --start-sidecar`)", err)
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
