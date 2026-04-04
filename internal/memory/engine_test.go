package memory_test

import (
	"fmt"
	"testing"

	"context0/internal/memory"
	"context0/internal/sidecar"
)

// newEngine creates a memory Engine backed by a temp project directory.
// It skips if the sidecar is not running (embeddings are required).
func newEngine(t *testing.T) *memory.Engine {
	t.Helper()
	if !sidecar.IsRunning() {
		t.Skip("sidecar not running — start with `context0 --start-sidecar`")
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

// ── SaveMemory ────────────────────────────────────────────────────────────────

// TestSaveMemoryPersistsAllFields verifies that every field (category, topic,
// content, timestamp) round-trips correctly through the database.
func TestSaveMemoryPersistsAllFields(t *testing.T) {
	eng := newEngine(t)

	doc, err := eng.SaveMemory("persistence", "field check", "all fields must survive a round-trip")
	if err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	if doc.Category != "persistence" {
		t.Errorf("Category: got %q, want %q", doc.Category, "persistence")
	}
	if doc.Topic != "field check" {
		t.Errorf("Topic: got %q, want %q", doc.Topic, "field check")
	}
	if doc.Content != "all fields must survive a round-trip" {
		t.Errorf("Content: got %q", doc.Content)
	}
	if doc.Timestamp.IsZero() {
		t.Error("Timestamp is zero — expected it to be populated")
	}
}

// TestSaveMemoryMultipleRetrievable saves several documents and verifies each
// is independently returned in a sufficiently broad query.
func TestSaveMemoryMultipleRetrievable(t *testing.T) {
	eng := newEngine(t)

	unique := []string{
		"xzqunique alpha content",
		"xzqunique beta content",
		"xzqunique gamma content",
	}
	ids := make([]int64, len(unique))
	for i, c := range unique {
		doc, err := eng.SaveMemory("cat", "topic", c)
		if err != nil {
			t.Fatalf("SaveMemory(%d): %v", i, err)
		}
		ids[i] = doc.ID
	}

	// All IDs should be distinct.
	seen := make(map[int64]bool)
	for _, id := range ids {
		if seen[id] {
			t.Errorf("duplicate ID %d", id)
		}
		seen[id] = true
	}

	// A broad query should surface all three.
	results, err := eng.QueryMemory("xzqunique content", 10)
	if err != nil {
		t.Fatalf("QueryMemory: %v", err)
	}
	found := make(map[int64]bool)
	for _, r := range results {
		found[r.ID] = true
	}
	for _, id := range ids {
		if !found[id] {
			t.Errorf("doc id=%d not returned by query", id)
		}
	}
}

// TestSaveMemoryCategoryIsIndexed verifies that category text is included in
// the FTS5 index and contributes to search results.
func TestSaveMemoryCategoryIsIndexed(t *testing.T) {
	eng := newEngine(t)

	doc, err := eng.SaveMemory("zxquniquecategory", "some topic", "some content")
	if err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	// Query using the unique category term.
	results, err := eng.QueryMemory("zxquniquecategory", 5)
	if err != nil {
		t.Fatalf("QueryMemory: %v", err)
	}
	found := false
	for _, r := range results {
		if r.ID == doc.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("doc with unique category not returned when querying by category term")
	}
}

// ── UpdateMemory ──────────────────────────────────────────────────────────────

// TestUpdateMemoryOnlyCategory updates only the category field and verifies
// topic and content are preserved unchanged.
func TestUpdateMemoryOnlyCategory(t *testing.T) {
	eng := newEngine(t)

	doc, err := eng.SaveMemory("old-cat", "the topic", "the content")
	if err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	updated, err := eng.UpdateMemory(doc.ID, "new-cat", "", "")
	if err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}
	if updated.Category != "new-cat" {
		t.Errorf("Category: got %q, want %q", updated.Category, "new-cat")
	}
	if updated.Topic != "the topic" {
		t.Errorf("Topic changed unexpectedly: got %q", updated.Topic)
	}
	if updated.Content != "the content" {
		t.Errorf("Content changed unexpectedly: got %q", updated.Content)
	}
}

// TestUpdateMemoryOnlyContent updates only the content field and verifies
// category and topic are preserved, and the new content is queryable.
func TestUpdateMemoryOnlyContent(t *testing.T) {
	eng := newEngine(t)

	doc, err := eng.SaveMemory("cat", "topic", "original wording here")
	if err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	_, err = eng.UpdateMemory(doc.ID, "", "", "completely revised wording zxqrevised")
	if err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}

	// New content should be findable.
	results, err := eng.QueryMemory("zxqrevised", 5)
	if err != nil {
		t.Fatalf("QueryMemory after update: %v", err)
	}
	found := false
	for _, r := range results {
		if r.ID == doc.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("updated content not found by query")
	}
}

// TestUpdateMemoryAllFields updates all three fields at once.
func TestUpdateMemoryAllFields(t *testing.T) {
	eng := newEngine(t)

	doc, err := eng.SaveMemory("cat-a", "topic-a", "content-a")
	if err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	updated, err := eng.UpdateMemory(doc.ID, "cat-b", "topic-b", "content-b")
	if err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}
	if updated.Category != "cat-b" || updated.Topic != "topic-b" || updated.Content != "content-b" {
		t.Errorf("unexpected updated doc: %+v", updated)
	}
}

// TestUpdateMemoryIDNotFound verifies that updating a non-existent ID returns
// an error.
func TestUpdateMemoryIDNotFound(t *testing.T) {
	eng := newEngine(t)

	_, err := eng.UpdateMemory(99999, "cat", "topic", "content")
	if err == nil {
		t.Fatal("expected error when updating non-existent ID, got nil")
	}
}

// ── DeleteMemory ──────────────────────────────────────────────────────────────

// TestDeleteMemoryIDNotFound verifies that deleting a non-existent ID is a
// silent no-op (SQLite DELETE of a missing row does not error).
func TestDeleteMemoryIDNotFound(t *testing.T) {
	eng := newEngine(t)

	if err := eng.DeleteMemory(99999); err != nil {
		t.Errorf("expected no error deleting non-existent ID, got: %v", err)
	}
}

// TestDeleteMemoryFTSEntryRemoved verifies the FTS5 trigger fires on delete:
// after deletion the doc must not appear in a keyword-only biased query.
func TestDeleteMemoryFTSEntryRemoved(t *testing.T) {
	eng := newEngine(t)

	// Use a unique term so only this doc would match the FTS5 leg.
	doc, err := eng.SaveMemory("tmp", "fts cleanup", "zxqftscleanupterm unique phrase")
	if err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	if err := eng.DeleteMemory(doc.ID); err != nil {
		t.Fatalf("DeleteMemory: %v", err)
	}

	results, err := eng.QueryMemory("zxqftscleanupterm unique phrase", 10)
	if err != nil {
		t.Fatalf("QueryMemory after delete: %v", err)
	}
	for _, r := range results {
		if r.ID == doc.ID {
			t.Errorf("deleted doc (id=%d) still returned by FTS query", doc.ID)
		}
	}
}

// ── QueryMemory ───────────────────────────────────────────────────────────────

// TestQueryMemoryTopKZeroDefaultsToThree verifies that topK=0 (and negative)
// falls back to the default of 3.
func TestQueryMemoryTopKZeroDefaultsToThree(t *testing.T) {
	eng := newEngine(t)

	// Save 5 documents.
	for i := range 5 {
		if _, err := eng.SaveMemory("cat", "topic", fmt.Sprintf("document number %d about zxqdefaulttopk", i)); err != nil {
			t.Fatalf("SaveMemory(%d): %v", i, err)
		}
	}

	results, err := eng.QueryMemory("zxqdefaulttopk", 0)
	if err != nil {
		t.Fatalf("QueryMemory(topK=0): %v", err)
	}
	if len(results) > 3 {
		t.Errorf("topK=0 should default to 3, got %d results", len(results))
	}
}

// TestQueryMemoryTopKLargerThanCorpus verifies that when topK exceeds the
// number of stored documents all documents are returned.
func TestQueryMemoryTopKLargerThanCorpus(t *testing.T) {
	eng := newEngine(t)

	for i := range 3 {
		if _, err := eng.SaveMemory("cat", "topic", fmt.Sprintf("corpus item %d zxqcorpus", i)); err != nil {
			t.Fatalf("SaveMemory(%d): %v", i, err)
		}
	}

	results, err := eng.QueryMemory("zxqcorpus", 100)
	if err != nil {
		t.Fatalf("QueryMemory(topK=100): %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results (full corpus), got %d", len(results))
	}
}

// TestQueryMemorySpecialCharactersInQuery verifies that double-quotes and
// other FTS5 metacharacters in the query string do not cause an error.
func TestQueryMemorySpecialCharactersInQuery(t *testing.T) {
	eng := newEngine(t)

	if _, err := eng.SaveMemory("code", "sql", `use "quotes" in queries`); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	// These should not panic or return a SQL error.
	tricky := []string{
		`"quoted"`,
		`say "hello" world`,
		`a "b" c "d"`,
		`a AND b OR c`,
		`*prefix*`,
	}
	for _, q := range tricky {
		if _, err := eng.QueryMemory(q, 5); err != nil {
			t.Errorf("QueryMemory(%q) returned unexpected error: %v", q, err)
		}
	}
}
