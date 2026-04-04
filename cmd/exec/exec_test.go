package exec_test

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	cmdexec "context0/cmd/exec"
	"context0/internal/sidecar"
)

// run executes the exec command tree with the given args and returns stdout.
// Because the commands write directly to os.Stdout, we redirect it via a pipe.
func run(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	root := &cobra.Command{Use: "context0"}
	root.AddCommand(cmdexec.NewCmd(&dir))
	root.SetArgs(append([]string{"exec"}, args...))
	execErr := root.Execute()

	w.Close()
	os.Stdout = origStdout

	out, _ := io.ReadAll(r)
	r.Close()
	return string(out), execErr
}

// mustRun calls run and fails on error.
func mustRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := run(t, dir, args...)
	if err != nil {
		t.Fatalf("exec %v: %v\noutput: %s", args, err, out)
	}
	return out
}

// requireSidecar skips the test if the sidecar is not running.
func requireSidecar(t *testing.T) {
	t.Helper()
	if !sidecar.IsRunning() {
		t.Skip("sidecar not running — skipping sidecar-dependent test")
	}
}

// ── inline script ─────────────────────────────────────────────────────────────

func TestExecInlinePrint(t *testing.T) {
	requireSidecar(t)
	dir := t.TempDir()
	out := mustRun(t, dir, `print("hello from exec test")`)
	if !strings.Contains(out, "hello from exec test") {
		t.Fatalf("unexpected exec output: %q", out)
	}
}

func TestExecInlineArithmetic(t *testing.T) {
	requireSidecar(t)
	dir := t.TempDir()
	out := mustRun(t, dir, `print(1 + 2)`)
	if !strings.Contains(out, "3") {
		t.Fatalf("expected output '3', got: %q", out)
	}
}

// ── file script ───────────────────────────────────────────────────────────────

func TestExecFile(t *testing.T) {
	requireSidecar(t)
	dir := t.TempDir()

	scriptPath := dir + "/test_script.py"
	if err := os.WriteFile(scriptPath, []byte(`print("from file")`), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	out := mustRun(t, dir, scriptPath)
	if !strings.Contains(out, "from file") {
		t.Fatalf("unexpected output from file script: %q", out)
	}
}

// ── missing argument ──────────────────────────────────────────────────────────

// TestExecNoArgs verifies that omitting the script argument returns an error.
// This does NOT require the sidecar — it's a cobra arg-count check.
func TestExecNoArgs(t *testing.T) {
	dir := t.TempDir()
	_, err := run(t, dir)
	if err == nil {
		t.Fatal("expected error when no script argument provided")
	}
}
