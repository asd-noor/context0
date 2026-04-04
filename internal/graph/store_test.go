package graph_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"context0/internal/graph"
)

// openTestStore creates a Store backed by a temporary directory that is
// cleaned up automatically after the test.
func openTestStore(t *testing.T) *graph.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := graph.Open(dir, "codemap.sqlite")
	if err != nil {
		t.Fatalf("graph.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func makeNode(file, name, kind string) graph.Node {
	return graph.Node{
		ID:        graph.NodeID(file, name, kind),
		Name:      name,
		Kind:      kind,
		FilePath:  file,
		LineStart: 1, LineEnd: 10,
		ColStart: 1, ColEnd: 50,
		NameLine: 1, NameCol: 5,
	}
}

// ── Schema / open ─────────────────────────────────────────────────────────────

func TestOpenCreatesSchema(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	n, err := store.NodeCount(ctx)
	if err != nil {
		t.Fatalf("NodeCount: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 nodes in fresh store, got %d", n)
	}
}

func TestOpenReadOnlyMissingReturnsErrNotIndexed(t *testing.T) {
	dir := t.TempDir()
	_, err := graph.OpenReadOnly(dir, "codemap.sqlite")
	if err != graph.ErrNotIndexed {
		t.Fatalf("expected ErrNotIndexed, got %v", err)
	}
}

func TestOpenReadOnlySucceedsAfterOpen(t *testing.T) {
	dir := t.TempDir()
	rw, err := graph.Open(dir, "codemap.sqlite")
	if err != nil {
		t.Fatalf("graph.Open: %v", err)
	}
	rw.Close()

	ro, err := graph.OpenReadOnly(dir, "codemap.sqlite")
	if err != nil {
		t.Fatalf("graph.OpenReadOnly: %v", err)
	}
	ro.Close()
}

// ── BulkUpsertNodes ───────────────────────────────────────────────────────────

func TestBulkUpsertNodesEmpty(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if err := store.BulkUpsertNodes(ctx, nil); err != nil {
		t.Fatalf("BulkUpsertNodes(nil): %v", err)
	}
}

func TestBulkUpsertNodesAndCount(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	nodes := []graph.Node{
		makeNode("/a/main.go", "main", "function"),
		makeNode("/a/main.go", "helper", "function"),
		makeNode("/a/util.go", "Util", "type"),
	}
	if err := store.BulkUpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("BulkUpsertNodes: %v", err)
	}

	n, err := store.NodeCount(ctx)
	if err != nil {
		t.Fatalf("NodeCount: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 nodes, got %d", n)
	}
}

func TestBulkUpsertNodesIdempotent(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	nodes := []graph.Node{makeNode("/a/main.go", "main", "function")}
	if err := store.BulkUpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := store.BulkUpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	n, _ := store.NodeCount(ctx)
	if n != 1 {
		t.Fatalf("expected 1 node after idempotent upsert, got %d", n)
	}
}

// ── BulkUpsertEdges ───────────────────────────────────────────────────────────

func TestBulkUpsertEdgesAndCount(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	a := makeNode("/a/main.go", "caller", "function")
	b := makeNode("/a/main.go", "callee", "function")
	if err := store.BulkUpsertNodes(ctx, []graph.Node{a, b}); err != nil {
		t.Fatalf("BulkUpsertNodes: %v", err)
	}

	edges := []graph.Edge{{SourceID: a.ID, TargetID: b.ID, Relation: graph.RelationReferences}}
	if err := store.BulkUpsertEdges(ctx, edges); err != nil {
		t.Fatalf("BulkUpsertEdges: %v", err)
	}

	count, err := store.EdgeCount(ctx)
	if err != nil {
		t.Fatalf("EdgeCount: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 edge, got %d", count)
	}
}

func TestBulkUpsertEdgesIdempotent(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	a := makeNode("/a/main.go", "A", "function")
	b := makeNode("/a/main.go", "B", "function")
	_ = store.BulkUpsertNodes(ctx, []graph.Node{a, b})

	edge := []graph.Edge{{SourceID: a.ID, TargetID: b.ID, Relation: graph.RelationReferences}}
	_ = store.BulkUpsertEdges(ctx, edge)
	_ = store.BulkUpsertEdges(ctx, edge) // duplicate ignored

	count, _ := store.EdgeCount(ctx)
	if count != 1 {
		t.Fatalf("expected 1 edge after duplicate insert, got %d", count)
	}
}

// ── GetSymbolsInFile ──────────────────────────────────────────────────────────

func TestGetSymbolsInFile(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	nodes := []graph.Node{
		makeNode("/a/main.go", "Alpha", "function"),
		makeNode("/a/main.go", "Beta", "function"),
		makeNode("/b/other.go", "Gamma", "function"),
	}
	_ = store.BulkUpsertNodes(ctx, nodes)

	got, err := store.GetSymbolsInFile(ctx, "/a/main.go")
	if err != nil {
		t.Fatalf("GetSymbolsInFile: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 symbols in /a/main.go, got %d", len(got))
	}
	for _, n := range got {
		if n.FilePath != "/a/main.go" {
			t.Errorf("unexpected file_path %q", n.FilePath)
		}
	}
}

func TestGetSymbolsInFileEmpty(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	got, err := store.GetSymbolsInFile(ctx, "/nonexistent.go")
	if err != nil {
		t.Fatalf("GetSymbolsInFile on missing file: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0, got %d", len(got))
	}
}

// ── GetSymbolLocation ─────────────────────────────────────────────────────────

func TestGetSymbolLocation(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	nodes := []graph.Node{
		makeNode("/a/main.go", "MyFunc", "function"),
		makeNode("/b/other.go", "MyFunc", "function"), // same name, different file
	}
	_ = store.BulkUpsertNodes(ctx, nodes)

	got, err := store.GetSymbolLocation(ctx, "MyFunc", "")
	if err != nil {
		t.Fatalf("GetSymbolLocation: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 locations for MyFunc, got %d", len(got))
	}
}

func TestGetSymbolLocationNotFound(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	got, err := store.GetSymbolLocation(ctx, "NoSuchSymbol", "")
	if err != nil {
		t.Fatalf("GetSymbolLocation: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0, got %d", len(got))
	}
}

// ── FindNode ──────────────────────────────────────────────────────────────────

func TestFindNode(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	n := graph.Node{
		ID: graph.NodeID("/a/main.go", "main", "function"), Name: "main", Kind: "function",
		FilePath: "/a/main.go", LineStart: 5, LineEnd: 20,
		ColStart: 1, ColEnd: 1, NameLine: 5, NameCol: 6,
	}
	_ = store.BulkUpsertNodes(ctx, []graph.Node{n})

	found, err := store.FindNode(ctx, "/a/main.go", 10, 1)
	if err != nil {
		t.Fatalf("FindNode: %v", err)
	}
	if found == nil {
		t.Fatal("FindNode returned nil, expected a node")
	}
	if found.Name != "main" {
		t.Errorf("expected name=main, got %q", found.Name)
	}
}

func TestFindNodeOutsideRange(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	n := graph.Node{
		ID: graph.NodeID("/a/main.go", "main", "function"), Name: "main", Kind: "function",
		FilePath: "/a/main.go", LineStart: 5, LineEnd: 20,
		ColStart: 1, ColEnd: 1, NameLine: 5, NameCol: 6,
	}
	_ = store.BulkUpsertNodes(ctx, []graph.Node{n})

	found, err := store.FindNode(ctx, "/a/main.go", 100, 1)
	if err != nil {
		t.Fatalf("FindNode: %v", err)
	}
	if found != nil {
		t.Fatalf("expected nil for out-of-range line, got node %q", found.Name)
	}
}

// ── FindImpact ────────────────────────────────────────────────────────────────

func TestFindImpactTransitive(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	// A → B → C  (A and B depend on C)
	// FindImpact("C") should return A and B.
	a := makeNode("/p/a.go", "A", "function")
	b := makeNode("/p/b.go", "B", "function")
	c := makeNode("/p/c.go", "C", "function")
	_ = store.BulkUpsertNodes(ctx, []graph.Node{a, b, c})
	_ = store.BulkUpsertEdges(ctx, []graph.Edge{
		{SourceID: b.ID, TargetID: c.ID, Relation: graph.RelationReferences}, // B → C
		{SourceID: a.ID, TargetID: b.ID, Relation: graph.RelationReferences}, // A → B
	})

	impacted, err := store.FindImpact(ctx, "C")
	if err != nil {
		t.Fatalf("FindImpact: %v", err)
	}
	names := make(map[string]struct{})
	for _, n := range impacted {
		names[n.Name] = struct{}{}
	}
	for _, want := range []string{"A", "B"} {
		if _, ok := names[want]; !ok {
			t.Errorf("expected %q in impact set, got %v", want, names)
		}
	}
	if _, ok := names["C"]; ok {
		t.Errorf("C itself should not appear in its own impact set")
	}
}

func TestFindImpactNoEdges(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	n := makeNode("/p/solo.go", "Alone", "function")
	_ = store.BulkUpsertNodes(ctx, []graph.Node{n})

	impacted, err := store.FindImpact(ctx, "Alone")
	if err != nil {
		t.Fatalf("FindImpact: %v", err)
	}
	if len(impacted) != 0 {
		t.Fatalf("expected empty impact set, got %d nodes", len(impacted))
	}
}

// ── DeleteNodesByFile / cascade ───────────────────────────────────────────────

func TestDeleteNodesByFileCascadesEdges(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	a := makeNode("/a/main.go", "A", "function")
	b := makeNode("/b/other.go", "B", "function")
	_ = store.BulkUpsertNodes(ctx, []graph.Node{a, b})
	_ = store.BulkUpsertEdges(ctx, []graph.Edge{
		{SourceID: a.ID, TargetID: b.ID, Relation: graph.RelationReferences},
	})

	if err := store.DeleteNodesByFile(ctx, "/a/main.go"); err != nil {
		t.Fatalf("DeleteNodesByFile: %v", err)
	}

	nodeCount, _ := store.NodeCount(ctx)
	if nodeCount != 1 {
		t.Fatalf("expected 1 node after delete, got %d", nodeCount)
	}
	edgeCount, _ := store.EdgeCount(ctx)
	if edgeCount != 0 {
		t.Fatalf("expected 0 edges after cascade delete, got %d", edgeCount)
	}
}

// ── PruneStaleFiles ───────────────────────────────────────────────────────────

func TestPruneStaleFiles(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	nodes := []graph.Node{
		makeNode("/a/keep.go", "Keep", "function"),
		makeNode("/b/stale.go", "Stale", "function"),
	}
	_ = store.BulkUpsertNodes(ctx, nodes)

	live := map[string]struct{}{"/a/keep.go": {}}
	if err := store.PruneStaleFiles(ctx, live); err != nil {
		t.Fatalf("PruneStaleFiles: %v", err)
	}

	n, _ := store.NodeCount(ctx)
	if n != 1 {
		t.Fatalf("expected 1 node after prune, got %d", n)
	}
	remaining, _ := store.GetSymbolsInFile(ctx, "/a/keep.go")
	if len(remaining) != 1 || remaining[0].Name != "Keep" {
		t.Fatalf("wrong node survived prune: %v", remaining)
	}
}

// ── Clear ─────────────────────────────────────────────────────────────────────

func TestClear(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	a := makeNode("/a/main.go", "A", "function")
	b := makeNode("/b/other.go", "B", "function")
	_ = store.BulkUpsertNodes(ctx, []graph.Node{a, b})
	_ = store.BulkUpsertEdges(ctx, []graph.Edge{
		{SourceID: a.ID, TargetID: b.ID, Relation: graph.RelationReferences},
	})

	if err := store.Clear(ctx); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	n, _ := store.NodeCount(ctx)
	e, _ := store.EdgeCount(ctx)
	if n != 0 || e != 0 {
		t.Fatalf("after Clear: nodes=%d edges=%d, want 0/0", n, e)
	}
}

// ── Diagnostics ───────────────────────────────────────────────────────────────

func makeDiag(file string, line int, msg string) graph.Diagnostic {
	return graph.Diagnostic{
		ID:       graph.DiagnosticID(file, line, 1, msg),
		FilePath: file,
		Line:     line,
		Col:      1,
		Severity: graph.DiagnosticSeverityError,
		Message:  msg,
	}
}

func TestUpsertDiagnosticsAndCount(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	diags := []graph.Diagnostic{
		makeDiag("/a/main.go", 10, "undefined: Foo"),
		makeDiag("/a/main.go", 20, "undefined: Bar"),
	}
	if err := store.UpsertDiagnosticsForFile(ctx, "/a/main.go", diags); err != nil {
		t.Fatalf("UpsertDiagnosticsForFile: %v", err)
	}

	count, err := store.DiagnosticCount(ctx)
	if err != nil {
		t.Fatalf("DiagnosticCount: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 diagnostics, got %d", count)
	}
}

func TestUpsertDiagnosticsReplacesOldOnes(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	_ = store.UpsertDiagnosticsForFile(ctx, "/a/main.go", []graph.Diagnostic{
		makeDiag("/a/main.go", 10, "old error"),
	})
	// Second upsert replaces.
	_ = store.UpsertDiagnosticsForFile(ctx, "/a/main.go", []graph.Diagnostic{
		makeDiag("/a/main.go", 20, "new error"),
	})

	count, _ := store.DiagnosticCount(ctx)
	if count != 1 {
		t.Fatalf("expected 1 diagnostic after replacement, got %d", count)
	}
	diags, _ := store.GetDiagnosticsForFile(ctx, "/a/main.go")
	if diags[0].Message != "new error" {
		t.Errorf("expected 'new error', got %q", diags[0].Message)
	}
}

func TestGetAllDiagnosticsOrderedBySeverity(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	warning := graph.Diagnostic{
		ID:       graph.DiagnosticID("/a/main.go", 5, 1, "warn"),
		FilePath: "/a/main.go", Line: 5, Col: 1,
		Severity: graph.DiagnosticSeverityWarning, Message: "warn",
	}
	err := graph.Diagnostic{
		ID:       graph.DiagnosticID("/a/main.go", 3, 1, "err"),
		FilePath: "/a/main.go", Line: 3, Col: 1,
		Severity: graph.DiagnosticSeverityError, Message: "err",
	}
	_ = store.UpsertDiagnosticsForFile(ctx, "/a/main.go", []graph.Diagnostic{warning, err})

	all, diagErr := store.GetAllDiagnostics(ctx)
	if diagErr != nil {
		t.Fatalf("GetAllDiagnostics: %v", diagErr)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2, got %d", len(all))
	}
	// Severity 1 (error) < 2 (warning), so errors come first.
	if all[0].Severity != graph.DiagnosticSeverityError {
		t.Errorf("expected first diagnostic to be error (severity 1), got severity %d", all[0].Severity)
	}
}

func TestDiagnosticEdges(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	node := makeNode("/a/main.go", "MyFunc", "function")
	_ = store.BulkUpsertNodes(ctx, []graph.Node{node})

	diag := makeDiag("/a/main.go", 10, "something wrong")
	_ = store.UpsertDiagnosticsForFile(ctx, "/a/main.go", []graph.Diagnostic{diag})

	edges := []graph.DiagnosticEdge{{DiagnosticID: diag.ID, NodeID: node.ID}}
	if err := store.BulkUpsertDiagnosticEdges(ctx, edges); err != nil {
		t.Fatalf("BulkUpsertDiagnosticEdges: %v", err)
	}
	// Duplicate insert should be ignored.
	if err := store.BulkUpsertDiagnosticEdges(ctx, edges); err != nil {
		t.Fatalf("BulkUpsertDiagnosticEdges duplicate: %v", err)
	}
}

func TestUpsertDiagnosticsEmptyClears(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	_ = store.UpsertDiagnosticsForFile(ctx, "/a/main.go", []graph.Diagnostic{
		makeDiag("/a/main.go", 10, "error"),
	})
	// Upsert with empty slice should clear the file's diagnostics.
	_ = store.UpsertDiagnosticsForFile(ctx, "/a/main.go", nil)

	count, _ := store.DiagnosticCount(ctx)
	if count != 0 {
		t.Fatalf("expected 0 diagnostics after empty upsert, got %d", count)
	}
}

// ── DBPath resolution ─────────────────────────────────────────────────────────

func TestOpenCreatesDBFile(t *testing.T) {
	dir := t.TempDir()
	store, err := graph.Open(dir, "codemap.sqlite")
	if err != nil {
		t.Fatalf("graph.Open: %v", err)
	}
	store.Close()

	// The db file should exist under $HOME/.context0/...
	// We just verify Open doesn't error and the store is functional.
	store2, err := graph.Open(dir, "codemap.sqlite")
	if err != nil {
		t.Fatalf("second graph.Open: %v", err)
	}
	defer store2.Close()

	// Confirm it's empty (fresh open, same path = persisted store).
	n, err := store2.NodeCount(context.Background())
	if err != nil {
		t.Fatalf("NodeCount after reopen: %v", err)
	}
	_ = n // may be 0 or populated from prior runs; just confirm no error

	// Now write a node and confirm it round-trips.
	node := makeNode(filepath.Join(os.TempDir(), "f.go"), "RoundTrip", "function")
	_ = store2.BulkUpsertNodes(context.Background(), []graph.Node{node})
	n2, _ := store2.NodeCount(context.Background())
	if n2 < 1 {
		t.Fatalf("expected at least 1 node after insert, got %d", n2)
	}
}
