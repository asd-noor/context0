package util_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"context0/util"
)

// ── NodeID ────────────────────────────────────────────────────────────────────

func TestNodeIDDeterministic(t *testing.T) {
	id1 := util.NodeID("/a/b/c.go", "MyFunc", "function")
	id2 := util.NodeID("/a/b/c.go", "MyFunc", "function")
	if id1 != id2 {
		t.Fatalf("NodeID not deterministic: %q != %q", id1, id2)
	}
}

func TestNodeIDUnique(t *testing.T) {
	cases := [][3]string{
		{"/a/b.go", "Foo", "function"},
		{"/a/b.go", "Foo", "method"},   // same name+file, different kind
		{"/a/b.go", "Bar", "function"}, // different name
		{"/a/c.go", "Foo", "function"}, // different file
	}
	seen := map[string]struct{}{}
	for _, c := range cases {
		id := util.NodeID(c[0], c[1], c[2])
		if _, dup := seen[id]; dup {
			t.Fatalf("NodeID collision for %v", c)
		}
		seen[id] = struct{}{}
	}
}

func TestNodeIDLength(t *testing.T) {
	id := util.NodeID("/some/path.go", "SomeSymbol", "function")
	// 16 bytes hex-encoded = 32 chars
	if len(id) != 32 {
		t.Fatalf("NodeID length = %d, want 32", len(id))
	}
}

// ── DiagnosticID ──────────────────────────────────────────────────────────────

func TestDiagnosticIDDeterministic(t *testing.T) {
	id1 := util.DiagnosticID("/a/b.go", 10, 5, "undefined: Foo")
	id2 := util.DiagnosticID("/a/b.go", 10, 5, "undefined: Foo")
	if id1 != id2 {
		t.Fatalf("DiagnosticID not deterministic: %q != %q", id1, id2)
	}
}

func TestDiagnosticIDDistinguishesLocation(t *testing.T) {
	base := util.DiagnosticID("/a/b.go", 10, 5, "msg")
	diffLine := util.DiagnosticID("/a/b.go", 11, 5, "msg")
	diffCol := util.DiagnosticID("/a/b.go", 10, 6, "msg")
	diffMsg := util.DiagnosticID("/a/b.go", 10, 5, "other msg")
	diffFile := util.DiagnosticID("/a/c.go", 10, 5, "msg")

	for _, other := range []string{diffLine, diffCol, diffMsg, diffFile} {
		if base == other {
			t.Fatalf("DiagnosticID collision: identical IDs for different inputs")
		}
	}
}

// ── PathToURI / URIToPath ─────────────────────────────────────────────────────

func TestPathToURIScheme(t *testing.T) {
	uri := util.PathToURI("/usr/local/src/main.go")
	if !strings.HasPrefix(uri, "file://") {
		t.Fatalf("PathToURI(%q) = %q, want file:// prefix", "/usr/local/src/main.go", uri)
	}
}

func TestPathToURIRoundTrip(t *testing.T) {
	path := "/usr/local/src/main.go"
	uri := util.PathToURI(path)
	got := util.URIToPath(uri)
	if got != path {
		t.Fatalf("round-trip: PathToURI→URIToPath: got %q, want %q", got, path)
	}
}

func TestURIToPathMalformed(t *testing.T) {
	// Should not panic; fallback strips "file://" prefix.
	got := util.URIToPath("file:///tmp/foo.go")
	if got != "/tmp/foo.go" {
		t.Fatalf("URIToPath(file:///tmp/foo.go) = %q, want /tmp/foo.go", got)
	}
}

// ── FindGitRoot ───────────────────────────────────────────────────────────────

func TestFindGitRootFindsRoot(t *testing.T) {
	// The context0 repo itself has a .git at the workspace root.
	// Walk up from a deep subdir and confirm we land on the repo root.
	sub := filepath.Join("..", "internal", "graph")
	root := util.FindGitRoot(sub)

	// The result must contain a .git directory.
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		t.Fatalf("FindGitRoot(%q) = %q — no .git found there: %v", sub, root, err)
	}
}

func TestFindGitRootNoGit(t *testing.T) {
	// A temp directory with no .git anywhere above it.
	dir := t.TempDir()
	got := util.FindGitRoot(dir)
	// Must return an absolute path and not panic.
	if !filepath.IsAbs(got) {
		t.Fatalf("FindGitRoot returned non-absolute path: %q", got)
	}
}
