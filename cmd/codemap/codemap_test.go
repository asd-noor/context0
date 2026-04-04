package codemap_test

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	cmdcodemap "context0/cmd/codemap"
	"context0/internal/db"
	"context0/internal/graph"
)

// ── package-level shared state ────────────────────────────────────────────────

// sharedProjectDir is the real project root (two dirs up from cmd/codemap),
// set once by TestMain. AfterIndex tests use the real $HOME so they query the
// actual index without any snapshot/restore overhead.
var sharedProjectDir string

// TestMain verifies that the pre-built codemap index exists, then runs the
// tests. AfterIndex tests share the real $HOME and real index; they are fast
// and test against meaningful production data.
//
// Before running the tests for the first time, build and index once:
//
//	go build -o /tmp/context0 . && /tmp/context0 codemap index
func TestMain(m *testing.M) {
	root, err := filepath.Abs("../../")
	if err != nil {
		panic("projectRoot: " + err.Error())
	}
	sharedProjectDir = root

	realDataDir, err := db.ProjectDir(root)
	if err != nil {
		panic("db.ProjectDir: " + err.Error())
	}
	prebuiltDB := filepath.Join(realDataDir, "codemap-ctx0.sqlite")
	if _, err := os.Stat(prebuiltDB); err != nil {
		panic(
			"codemap index not found at " + prebuiltDB + "\n" +
				"Run once before testing:\n" +
				"  go build -o /tmp/context0 . && /tmp/context0 codemap index",
		)
	}

	os.Exit(m.Run())
}

// ── helpers ───────────────────────────────────────────────────────────────────

// run executes the codemap command tree with the given args and returns stdout.
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
	root.PersistentFlags().String("project", dir, "project dir")
	projectDir := dir
	root.AddCommand(cmdcodemap.NewCmd(&projectDir))
	root.SetArgs(append([]string{"codemap"}, args...))
	execErr := root.Execute()

	w.Close()
	os.Stdout = origStdout

	out, _ := io.ReadAll(r)
	r.Close()
	return string(out), execErr
}

// mustRun calls run and fails the test on error.
func mustRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := run(t, dir, args...)
	if err != nil {
		t.Fatalf("codemap %v: %v\noutput: %s", args, err, out)
	}
	return out
}

// mustRunShared runs a command against the shared pre-indexed project using
// the real $HOME, so the real index is found without any snapshot/restore.
func mustRunShared(t *testing.T, args ...string) string {
	t.Helper()
	out, err := run(t, sharedProjectDir, args...)
	if err != nil {
		t.Fatalf("codemap %v: %v\noutput: %s", args, err, out)
	}
	return out
}

// projectRoot returns the absolute path to the context0 repository root
// (two directories up from cmd/codemap).
func projectRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../")
	if err != nil {
		t.Fatalf("projectRoot: %v", err)
	}
	return root
}

// setupGoProject redirects HOME to an isolated temp dir so writes land there
// instead of the real ~/.context0. Uses os.MkdirTemp + best-effort cleanup to
// avoid t.TempDir()'s behaviour of failing the test when macOS creates a
// ~/Library directory under the temp HOME that cannot be removed.
func setupGoProject(t *testing.T) string {
	t.Helper()
	tmpHome, err := os.MkdirTemp("", "ctx0-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	origHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmpHome); err != nil {
		t.Fatalf("Setenv HOME: %v", err)
	}
	t.Cleanup(func() {
		os.Setenv("HOME", origHome) //nolint:errcheck
		os.RemoveAll(tmpHome)       // best-effort; macOS Library remnants are harmless
	})
	return projectRoot(t)
}

// ── db naming unit tests ──────────────────────────────────────────────────────

// TestIndexCreatesDB verifies that after running `codemap index` the standard
// database file "codemap-ctx0.sqlite" is created inside the project's
// context0 data directory.
func TestIndexCreatesDB(t *testing.T) {
	dir := setupGoProject(t)

	mustRun(t, dir, "index")

	projectDir, err := db.ProjectDir(dir)
	if err != nil {
		t.Fatalf("db.ProjectDir: %v", err)
	}
	expectedDB := filepath.Join(projectDir, "codemap-ctx0.sqlite")
	if _, err := os.Stat(expectedDB); err != nil {
		t.Fatalf("expected DB at %q not found: %v", expectedDB, err)
	}
}

// ── no-index error cases ──────────────────────────────────────────────────────

func TestStatusNoIndex(t *testing.T) {
	dir := setupGoProject(t)
	_, err := run(t, dir, "status")
	if err == nil {
		t.Fatal("expected error from status on unindexed project, got nil")
	}
	if !isNotIndexed(err) {
		t.Fatalf("expected ErrNotIndexed, got: %v", err)
	}
}

func TestOutlineNoIndex(t *testing.T) {
	dir := setupGoProject(t)
	_, err := run(t, dir, "outline", filepath.Join(dir, "internal", "graph", "hash.go"))
	if err == nil {
		t.Fatal("expected error from outline on unindexed project, got nil")
	}
}

func TestFindNoIndex(t *testing.T) {
	dir := setupGoProject(t)
	_, err := run(t, dir, "find", "NodeID")
	if err == nil {
		t.Fatal("expected error from find on unindexed project, got nil")
	}
}

func TestImpactNoIndex(t *testing.T) {
	dir := setupGoProject(t)
	_, err := run(t, dir, "impact", "NodeID")
	if err == nil {
		t.Fatal("expected error from impact on unindexed project, got nil")
	}
}

func TestDiagnosticsNoIndex(t *testing.T) {
	dir := setupGoProject(t)
	_, err := run(t, dir, "diagnostics")
	if err == nil {
		t.Fatal("expected error from diagnostics on unindexed project, got nil")
	}
}

// ── index + read commands ─────────────────────────────────────────────────────

func TestIndexOutputsNodeCount(t *testing.T) {
	dir := setupGoProject(t)
	out := mustRun(t, dir, "index")
	if !strings.Contains(out, "indexed") || !strings.Contains(out, "nodes=") {
		t.Fatalf("unexpected index output: %q", out)
	}
	// Must have indexed at least one node (real project has many symbols).
	if strings.Contains(out, "nodes=0") {
		t.Fatalf("index found 0 nodes: %q", out)
	}
}

func TestStatusAfterIndex(t *testing.T) {
	out := mustRunShared(t, "status")
	if !strings.Contains(out, "nodes=") {
		t.Fatalf("status output missing nodes=: %q", out)
	}
	if !strings.Contains(out, "edges=") {
		t.Fatalf("status output missing edges=: %q", out)
	}
}

func TestOutlineAfterIndex(t *testing.T) {
	out := mustRunShared(t, "outline", filepath.Join(sharedProjectDir, "internal", "graph", "hash.go"))
	for _, sym := range []string{"NodeID", "DiagnosticID"} {
		if !strings.Contains(out, sym) {
			t.Errorf("outline output missing symbol %q: %q", sym, out)
		}
	}
}

func TestFindAfterIndex(t *testing.T) {
	out := mustRunShared(t, "find", "NodeID")
	if !strings.Contains(out, "NodeID") {
		t.Fatalf("find output missing symbol: %q", out)
	}
	// Should show the file.
	if !strings.Contains(out, "hash.go") {
		t.Fatalf("find output missing file reference: %q", out)
	}
}

func TestDiagnosticsAfterIndex(t *testing.T) {
	// Expect no error; output contents may vary.
	out := mustRunShared(t, "diagnostics")
	_ = out
}

// ── JSON output ───────────────────────────────────────────────────────────────

func TestOutlineJSONAfterIndex(t *testing.T) {
	out := mustRunShared(t, "outline", "--json", filepath.Join(sharedProjectDir, "internal", "graph", "hash.go"))
	var nodes []map[string]any
	if err := json.Unmarshal([]byte(out), &nodes); err != nil {
		t.Fatalf("outline --json: invalid JSON: %v\noutput: %q", err, out)
	}
}

func TestFindJSONAfterIndex(t *testing.T) {
	out := mustRunShared(t, "find", "--json", "NodeID")
	var results []map[string]any
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("find --json: invalid JSON: %v\noutput: %q", err, out)
	}
}

func TestImpactJSONAfterIndex(t *testing.T) {
	out := mustRunShared(t, "impact", "--json", "NodeID")
	// impact returns an array (possibly empty).
	var results []map[string]any
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("impact --json: invalid JSON: %v\noutput: %q", err, out)
	}
}

func TestDiagnosticsJSONAfterIndex(t *testing.T) {
	out := mustRunShared(t, "diagnostics", "--json")
	// Returns an array (possibly null/empty).
	if !strings.HasPrefix(strings.TrimSpace(out), "[") && !strings.HasPrefix(strings.TrimSpace(out), "null") {
		t.Fatalf("diagnostics --json: expected JSON array, got: %q", out)
	}
}

// ── find with --source flag ───────────────────────────────────────────────────

func TestFindWithSourceAfterIndex(t *testing.T) {
	out := mustRunShared(t, "find", "--source", "NodeID")
	// Should contain the symbol name from the source snippet.
	if !strings.Contains(out, "NodeID") {
		t.Fatalf("find --source output missing symbol: %q", out)
	}
}

// ── helper ────────────────────────────────────────────────────────────────────

func isNotIndexed(err error) bool {
	if err == nil {
		return false
	}
	return err == graph.ErrNotIndexed || strings.Contains(err.Error(), "not indexed") || strings.Contains(err.Error(), "no such table")
}
