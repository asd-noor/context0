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

// setupGoProject creates a temp dir with a minimal Go file and a .git stub so
// that util.FindGitRoot resolves to dir (not a parent). Returns the dir.
func setupGoProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Create a .git directory so FindGitRoot stops here.
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	// Write a simple Go source file.
	src := `package hello

// Greeter greets people.
type Greeter struct{}

// Hello returns a greeting string.
func Hello(name string) string {
	return "Hello, " + name
}

// Goodbye says farewell.
func Goodbye(name string) string {
	return "Goodbye, " + name
}
`
	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write hello.go: %v", err)
	}
	return dir
}

// indexedProject creates a project and runs `codemap index` on it.
func indexedProject(t *testing.T) string {
	t.Helper()
	dir := setupGoProject(t)
	mustRun(t, dir, "index")
	return dir
}

// ── db naming unit tests ──────────────────────────────────────────────────────

// TestPersistentPreRunEDefaultsSrcRoot verifies that after PersistentPreRunE
// fires, the DB is named "<basename>-ctx0.sqlite". We verify this by checking
// the DB file is created in the right place after index.
func TestIndexCreatesDBNamedAfterProject(t *testing.T) {
	dir := setupGoProject(t)
	base := filepath.Base(dir)

	mustRun(t, dir, "index")

	// Determine where the DB should live.
	projectDir, err := db.ProjectDir(dir)
	if err != nil {
		t.Fatalf("db.ProjectDir: %v", err)
	}
	expectedDB := filepath.Join(projectDir, base+"-ctx0.sqlite")
	if _, err := os.Stat(expectedDB); err != nil {
		t.Fatalf("expected DB at %q not found: %v", expectedDB, err)
	}
}

// TestSrcRootBareNameOverridesDBName verifies that --src-root myrepo produces
// "myrepo-ctx0.sqlite", regardless of the project basename.
func TestSrcRootBareNameOverridesDBName(t *testing.T) {
	dir := setupGoProject(t)
	customName := "customrepo"

	mustRun(t, dir, "--src-root", customName, "index")

	projectDir, err := db.ProjectDir(dir)
	if err != nil {
		t.Fatalf("db.ProjectDir: %v", err)
	}
	expectedDB := filepath.Join(projectDir, customName+"-ctx0.sqlite")
	if _, err := os.Stat(expectedDB); err != nil {
		t.Fatalf("expected DB at %q not found: %v", expectedDB, err)
	}
}

// TestTwoSrcRootsProduceTwoDBFiles verifies that two different --src-root
// values give independent database files.
func TestTwoSrcRootsProduceTwoDBFiles(t *testing.T) {
	dir := setupGoProject(t)

	mustRun(t, dir, "--src-root", "alpha", "index")
	mustRun(t, dir, "--src-root", "beta", "index")

	projectDir, err := db.ProjectDir(dir)
	if err != nil {
		t.Fatalf("db.ProjectDir: %v", err)
	}
	for _, name := range []string{"alpha-ctx0.sqlite", "beta-ctx0.sqlite"} {
		p := filepath.Join(projectDir, name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected DB %q not found: %v", p, err)
		}
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
	_, err := run(t, dir, "outline", filepath.Join(dir, "hello.go"))
	if err == nil {
		t.Fatal("expected error from outline on unindexed project, got nil")
	}
}

func TestFindNoIndex(t *testing.T) {
	dir := setupGoProject(t)
	_, err := run(t, dir, "find", "Hello")
	if err == nil {
		t.Fatal("expected error from find on unindexed project, got nil")
	}
}

func TestImpactNoIndex(t *testing.T) {
	dir := setupGoProject(t)
	_, err := run(t, dir, "impact", "Hello")
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
	// Must have indexed at least one node (Hello, Goodbye, Greeter).
	if strings.Contains(out, "nodes=0") {
		t.Fatalf("index found 0 nodes: %q", out)
	}
}

func TestStatusAfterIndex(t *testing.T) {
	dir := indexedProject(t)
	out := mustRun(t, dir, "status")
	if !strings.Contains(out, "nodes=") {
		t.Fatalf("status output missing nodes=: %q", out)
	}
	if !strings.Contains(out, "edges=") {
		t.Fatalf("status output missing edges=: %q", out)
	}
}

func TestOutlineAfterIndex(t *testing.T) {
	dir := indexedProject(t)
	out := mustRun(t, dir, "outline", filepath.Join(dir, "hello.go"))
	for _, sym := range []string{"Hello", "Goodbye", "Greeter"} {
		if !strings.Contains(out, sym) {
			t.Errorf("outline output missing symbol %q: %q", sym, out)
		}
	}
}

func TestFindAfterIndex(t *testing.T) {
	dir := indexedProject(t)
	out := mustRun(t, dir, "find", "Hello")
	if !strings.Contains(out, "Hello") {
		t.Fatalf("find output missing symbol: %q", out)
	}
	// Should show the file.
	if !strings.Contains(out, "hello.go") {
		t.Fatalf("find output missing file reference: %q", out)
	}
}

func TestDiagnosticsAfterIndex(t *testing.T) {
	dir := indexedProject(t)
	// The simple hello.go has no errors — expect "no diagnostics found" or a
	// valid diagnostics table. Either is acceptable; just must not error.
	out := mustRun(t, dir, "diagnostics")
	_ = out
}

// ── JSON output ───────────────────────────────────────────────────────────────

func TestOutlineJSONAfterIndex(t *testing.T) {
	dir := indexedProject(t)
	out := mustRun(t, dir, "outline", "--json", filepath.Join(dir, "hello.go"))
	var nodes []map[string]any
	if err := json.Unmarshal([]byte(out), &nodes); err != nil {
		t.Fatalf("outline --json: invalid JSON: %v\noutput: %q", err, out)
	}
}

func TestFindJSONAfterIndex(t *testing.T) {
	dir := indexedProject(t)
	out := mustRun(t, dir, "find", "--json", "Hello")
	var results []map[string]any
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("find --json: invalid JSON: %v\noutput: %q", err, out)
	}
}

func TestImpactJSONAfterIndex(t *testing.T) {
	dir := indexedProject(t)
	out := mustRun(t, dir, "impact", "--json", "Hello")
	// impact returns an array (possibly empty).
	var results []map[string]any
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("impact --json: invalid JSON: %v\noutput: %q", err, out)
	}
}

func TestDiagnosticsJSONAfterIndex(t *testing.T) {
	dir := indexedProject(t)
	out := mustRun(t, dir, "diagnostics", "--json")
	// Returns an array (possibly null/empty).
	if !strings.HasPrefix(strings.TrimSpace(out), "[") && !strings.HasPrefix(strings.TrimSpace(out), "null") {
		t.Fatalf("diagnostics --json: expected JSON array, got: %q", out)
	}
}

// ── find with --source flag ───────────────────────────────────────────────────

func TestFindWithSourceAfterIndex(t *testing.T) {
	dir := indexedProject(t)
	out := mustRun(t, dir, "find", "--source", "Hello")
	// Should contain the function keyword from the source snippet.
	if !strings.Contains(out, "Hello") {
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
