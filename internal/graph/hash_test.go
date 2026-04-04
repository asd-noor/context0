package graph_test

import (
	"testing"

	"context0/internal/graph"
)

// ── NodeID ────────────────────────────────────────────────────────────────────

func TestNodeIDDeterministic(t *testing.T) {
	id1 := graph.NodeID("/a/b/c.go", "MyFunc", "function")
	id2 := graph.NodeID("/a/b/c.go", "MyFunc", "function")
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
		id := graph.NodeID(c[0], c[1], c[2])
		if _, dup := seen[id]; dup {
			t.Fatalf("NodeID collision for %v", c)
		}
		seen[id] = struct{}{}
	}
}

func TestNodeIDLength(t *testing.T) {
	id := graph.NodeID("/some/path.go", "SomeSymbol", "function")
	// 16 bytes hex-encoded = 32 chars
	if len(id) != 32 {
		t.Fatalf("NodeID length = %d, want 32", len(id))
	}
}

// ── DiagnosticID ──────────────────────────────────────────────────────────────

func TestDiagnosticIDDeterministic(t *testing.T) {
	id1 := graph.DiagnosticID("/a/b.go", 10, 5, "undefined: Foo")
	id2 := graph.DiagnosticID("/a/b.go", 10, 5, "undefined: Foo")
	if id1 != id2 {
		t.Fatalf("DiagnosticID not deterministic: %q != %q", id1, id2)
	}
}

func TestDiagnosticIDDistinguishesLocation(t *testing.T) {
	base := graph.DiagnosticID("/a/b.go", 10, 5, "msg")
	diffLine := graph.DiagnosticID("/a/b.go", 11, 5, "msg")
	diffCol := graph.DiagnosticID("/a/b.go", 10, 6, "msg")
	diffMsg := graph.DiagnosticID("/a/b.go", 10, 5, "other msg")
	diffFile := graph.DiagnosticID("/a/c.go", 10, 5, "msg")

	for _, other := range []string{diffLine, diffCol, diffMsg, diffFile} {
		if base == other {
			t.Fatalf("DiagnosticID collision: identical IDs for different inputs")
		}
	}
}
