package memory_test

import (
	"testing"

	"context0/internal/memory"
	"context0/internal/sidecar"
)

// newEngine creates a memory Engine backed by a temp project directory.
// It skips if the sidecar is not running (embeddings are required).
func newEngine(t *testing.T) *memory.Engine {
	t.Helper()
	if !sidecar.IsRunning() {
		t.Skip("sidecar not running — start with `context0 --daemon`")
	}
	dir := t.TempDir()
	eng, err := memory.New(dir)
	if err != nil {
		t.Fatalf("memory.New: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	return eng
}

// TestSaveAndQueryMemory saves a document and retrieves it via keyword search.
func TestSaveAndQueryMemory(t *testing.T) {
	eng := newEngine(t)

	doc, err := eng.SaveMemory("testing", "go basics", "Go uses goroutines for concurrency")
	if err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}
	if doc.ID <= 0 {
		t.Fatalf("expected positive ID, got %d", doc.ID)
	}
	if doc.Category != "testing" || doc.Topic != "go basics" {
		t.Errorf("unexpected doc fields: %+v", doc)
	}

	results, err := eng.QueryMemory("goroutines concurrency", 5)
	if err != nil {
		t.Fatalf("QueryMemory: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result, got none")
	}
	found := false
	for _, r := range results {
		if r.ID == doc.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("saved doc (id=%d) not found in query results", doc.ID)
	}
}

// TestQueryMemoryTopK verifies topK limits the result set.
func TestQueryMemoryTopK(t *testing.T) {
	eng := newEngine(t)

	topics := []string{"alpha topic", "beta topic", "gamma topic", "delta topic"}
	for _, tp := range topics {
		if _, err := eng.SaveMemory("cat", tp, "content about "+tp); err != nil {
			t.Fatalf("SaveMemory(%s): %v", tp, err)
		}
	}

	results, err := eng.QueryMemory("topic", 2)
	if err != nil {
		t.Fatalf("QueryMemory: %v", err)
	}
	if len(results) > 2 {
		t.Errorf("topK=2: expected ≤2 results, got %d", len(results))
	}
}

// TestUpdateMemory saves a doc then updates its content.
func TestUpdateMemory(t *testing.T) {
	eng := newEngine(t)

	doc, err := eng.SaveMemory("arch", "database", "we use SQLite")
	if err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	updated, err := eng.UpdateMemory(doc.ID, "", "database choice", "we use PostgreSQL now")
	if err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}
	if updated.Topic != "database choice" {
		t.Errorf("topic not updated: got %q", updated.Topic)
	}
	if updated.Content != "we use PostgreSQL now" {
		t.Errorf("content not updated: got %q", updated.Content)
	}
	// Category unchanged.
	if updated.Category != "arch" {
		t.Errorf("category changed unexpectedly: got %q", updated.Category)
	}
}

// TestDeleteMemory saves a doc then deletes it, verifying it is gone.
func TestDeleteMemory(t *testing.T) {
	eng := newEngine(t)

	doc, err := eng.SaveMemory("tmp", "ephemeral", "this will be deleted")
	if err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	if err := eng.DeleteMemory(doc.ID); err != nil {
		t.Fatalf("DeleteMemory: %v", err)
	}

	// Query should not return the deleted doc.
	results, err := eng.QueryMemory("ephemeral deleted", 10)
	if err != nil {
		t.Fatalf("QueryMemory after delete: %v", err)
	}
	for _, r := range results {
		if r.ID == doc.ID {
			t.Errorf("deleted doc (id=%d) still appears in query results", doc.ID)
		}
	}
}

// TestQueryMemoryEmpty verifies QueryMemory on an empty DB returns no results.
func TestQueryMemoryEmpty(t *testing.T) {
	eng := newEngine(t)

	results, err := eng.QueryMemory("anything", 5)
	if err != nil {
		t.Fatalf("QueryMemory on empty DB: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results on empty DB, got %d", len(results))
	}
}

// TestSaveMemoryResultsAreSortedByScore verifies query results are ordered
// by descending RRF score (first result is most relevant).
func TestSaveMemoryResultsAreSortedByScore(t *testing.T) {
	eng := newEngine(t)

	// Save a highly relevant and a weakly related document.
	if _, err := eng.SaveMemory("code", "channels", "Go channels are used for goroutine communication"); err != nil {
		t.Fatalf("SaveMemory channels: %v", err)
	}
	if _, err := eng.SaveMemory("food", "pizza", "pizza is a popular Italian dish with cheese and tomato"); err != nil {
		t.Fatalf("SaveMemory pizza: %v", err)
	}

	results, err := eng.QueryMemory("goroutine channels communication", 5)
	if err != nil {
		t.Fatalf("QueryMemory: %v", err)
	}
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted: results[%d].Score=%f > results[%d].Score=%f",
				i, results[i].Score, i-1, results[i-1].Score)
		}
	}
}
