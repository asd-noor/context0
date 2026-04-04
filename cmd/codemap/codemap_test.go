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
	"context0/internal/archive"
	"context0/internal/db"
	"context0/internal/graph"
)

// ── package-level shared state ────────────────────────────────────────────────

// sharedHome is set once by TestMain to a temp directory that acts as $HOME
// for all tests that share the pre-built index.
var sharedHome string

// sharedProjectDir is the real project root, set once by TestMain.
var sharedProjectDir string

// TestMain locates the pre-built codemap index in the developer's real HOME,
// snapshots it into a temporary archive, then restores it into an isolated
// temp HOME so all "AfterIndex" tests can share a fast, clean copy without
// re-running the expensive index step.
//
// Before running the tests for the first time, build and index once:
//
//	go build -o /tmp/context0 . && /tmp/context0 codemap index
func TestMain(m *testing.M) {
	// Resolve the real project root (two dirs up from cmd/codemap).
	root, err := filepath.Abs("../../")
	if err != nil {
		panic("projectRoot: " + err.Error())
	}
	sharedProjectDir = root

	// Locate the pre-built index in the developer's real HOME.
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

	// Snapshot the pre-built index into a temp archive.
	snapFile, err := os.CreateTemp("", "ctx0-test-snap-*.tar.gz")
	if err != nil {
		panic("CreateTemp: " + err.Error())
	}
	snapshotPath := snapFile.Name()
	snapFile.Close()
	defer os.Remove(snapshotPath)

	if err := archive.Write(snapshotPath, []string{prebuiltDB}); err != nil {
		panic("archive snapshot: " + err.Error())
	}

	// Create a shared temp HOME and restore the snapshot into it.
	tmpHome, err := os.MkdirTemp("", "ctx0-test-home-*")
	if err != nil {
		panic("MkdirTemp: " + err.Error())
	}
	sharedHome = tmpHome
	defer os.RemoveAll(tmpHome)

	// Resolve the shared data dir by temporarily pointing HOME at tmpHome so
	// that db.ProjectDir constructs the path (and creates the directory) there.
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	sharedDataDir, err := db.ProjectDir(root)
	if err != nil {
		panic("db.ProjectDir (shared home): " + err.Error())
	}
	os.Setenv("HOME", origHome)

	if _, err := archive.Extract(sharedDataDir, snapshotPath); err != nil {
		panic("archive restore: " + err.Error())
	}

	os.Exit(m.Run())
}

// ── helpers ───────────────────────────────────────────────────────────────────

// runWithHome executes the codemap command tree with a specific HOME value.
func runWithHome(home, dir string, args ...string) (string, error) {
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", home)
	defer os.Setenv("HOME", origHome)

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		panic("os.Pipe: " + err.Error())
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

// mustRunShared runs a command against the shared pre-indexed project (using
// sharedHome so the DB is found).
func mustRunShared(t *testing.T, args ...string) string {
	t.Helper()
	out, err := runWithHome(sharedHome, sharedProjectDir, args...)
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

// setupGoProject redirects HOME to an isolated temp dir (so that the DB is
// written there instead of the real ~/.context0) and returns the real project
// root as the source directory.
func setupGoProject(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
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
