package memory_test

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	cmdmemory "context0/cmd/memory"
	"context0/internal/sidecar"
)

// run executes the memory command tree with the given args and returns stdout.
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
	root.AddCommand(cmdmemory.NewCmd(&dir))
	root.SetArgs(append([]string{"memory"}, args...))
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
		t.Fatalf("memory %v: %v\noutput: %s", args, err, out)
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

// ── save ──────────────────────────────────────────────────────────────────────

func TestMemorySave(t *testing.T) {
	requireSidecar(t)
	dir := t.TempDir()
	out := mustRun(t, dir, "save",
		"--category", "architecture",
		"--topic", "test topic",
		"--content", "test content for unit test",
	)
	if !strings.Contains(out, "saved memory id=") {
		t.Fatalf("unexpected save output: %q", out)
	}
}

func TestMemorySaveMissingFlags(t *testing.T) {
	requireSidecar(t)
	dir := t.TempDir()
	// Missing --content
	_, err := run(t, dir, "save", "--category", "cat", "--topic", "top")
	if err == nil {
		t.Fatal("expected error when --content is missing")
	}
}

// ── query ─────────────────────────────────────────────────────────────────────

func TestMemoryQueryEmpty(t *testing.T) {
	requireSidecar(t)
	dir := t.TempDir()
	out := mustRun(t, dir, "query", "anything")
	if !strings.Contains(out, "no results found") {
		t.Fatalf("expected 'no results found', got: %q", out)
	}
}

func TestMemoryQueryReturnsResult(t *testing.T) {
	requireSidecar(t)
	dir := t.TempDir()
	mustRun(t, dir, "save",
		"--category", "fix",
		"--topic", "unique-xyzzy-topic",
		"--content", "unique-xyzzy content for retrieval test",
	)
	out := mustRun(t, dir, "query", "unique-xyzzy")
	if !strings.Contains(out, "unique-xyzzy") {
		t.Fatalf("query did not return expected content: %q", out)
	}
}

func TestMemoryQueryMinimalFlag(t *testing.T) {
	requireSidecar(t)
	dir := t.TempDir()
	mustRun(t, dir, "save",
		"--category", "feature",
		"--topic", "compact test",
		"--content", "compact content",
	)
	out := mustRun(t, dir, "query", "--minimal", "compact")
	// --minimal uses tabwriter with ID CATEGORY TOPIC SCORE CONTENT header
	if !strings.Contains(out, "ID") || !strings.Contains(out, "CATEGORY") {
		t.Fatalf("--minimal output missing table header: %q", out)
	}
}

// ── update ────────────────────────────────────────────────────────────────────

func TestMemoryUpdate(t *testing.T) {
	requireSidecar(t)
	dir := t.TempDir()
	// Save a memory to get id=1
	mustRun(t, dir, "save",
		"--category", "architecture",
		"--topic", "original topic",
		"--content", "original content",
	)
	out := mustRun(t, dir, "update", "1", "--topic", "updated topic")
	if !strings.Contains(out, "updated memory id=1") {
		t.Fatalf("unexpected update output: %q", out)
	}
}

func TestMemoryUpdateNoFlags(t *testing.T) {
	requireSidecar(t)
	dir := t.TempDir()
	mustRun(t, dir, "save",
		"--category", "architecture",
		"--topic", "topic",
		"--content", "content",
	)
	_, err := run(t, dir, "update", "1")
	if err == nil {
		t.Fatal("expected error when no update flags provided")
	}
}

func TestMemoryUpdateInvalidID(t *testing.T) {
	requireSidecar(t)
	dir := t.TempDir()
	_, err := run(t, dir, "update", "notanid", "--topic", "new")
	if err == nil {
		t.Fatal("expected error for non-numeric id")
	}
}

// ── delete ────────────────────────────────────────────────────────────────────

func TestMemoryDelete(t *testing.T) {
	requireSidecar(t)
	dir := t.TempDir()
	mustRun(t, dir, "save",
		"--category", "architecture",
		"--topic", "to delete",
		"--content", "will be deleted",
	)
	out := mustRun(t, dir, "delete", "1")
	if !strings.Contains(out, "deleted memory id=1") {
		t.Fatalf("unexpected delete output: %q", out)
	}
}

func TestMemoryDeleteInvalidID(t *testing.T) {
	requireSidecar(t)
	dir := t.TempDir()
	_, err := run(t, dir, "delete", "notanid")
	if err == nil {
		t.Fatal("expected error for non-numeric id")
	}
}
