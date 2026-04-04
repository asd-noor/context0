package memory_test

import (
	"testing"

	"context0/internal/memory"
)

// ── MergeRRF ──────────────────────────────────────────────────────────────────

func TestMergeRRFBothEmpty(t *testing.T) {
	results := memory.MergeRRF(nil, nil)
	if len(results) != 0 {
		t.Fatalf("expected empty results, got %d", len(results))
	}
}

func TestMergeRRFKeywordOnly(t *testing.T) {
	fts := []int64{10, 20, 30}
	results := memory.MergeRRF(fts, nil)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	// First result must be ID 10 (rank 0 → highest score).
	if results[0].ID != 10 {
		t.Fatalf("expected first result ID=10, got %d", results[0].ID)
	}
	// All should have RankVec == -1 (absent from vector list).
	for _, r := range results {
		if r.RankVec != -1 {
			t.Errorf("ID=%d: expected RankVec=-1, got %d", r.ID, r.RankVec)
		}
	}
}

func TestMergeRRFVectorOnly(t *testing.T) {
	vec := []int64{5, 6, 7}
	results := memory.MergeRRF(nil, vec)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0].ID != 5 {
		t.Fatalf("expected first result ID=5, got %d", results[0].ID)
	}
	for _, r := range results {
		if r.RankFTS != -1 {
			t.Errorf("ID=%d: expected RankFTS=-1, got %d", r.ID, r.RankFTS)
		}
	}
}

func TestMergeRRFOverlapBoostsScore(t *testing.T) {
	// ID 1 appears in both lists at rank 0 → should outscore ID 2 (fts only).
	fts := []int64{1, 2}
	vec := []int64{1, 3}
	results := memory.MergeRRF(fts, vec)

	// Find scores by ID.
	scores := make(map[int64]float64)
	for _, r := range results {
		scores[r.ID] = r.Score
	}

	if scores[1] <= scores[2] {
		t.Errorf("overlapping ID 1 (score=%.6f) should outscore keyword-only ID 2 (score=%.6f)", scores[1], scores[2])
	}
	if scores[1] <= scores[3] {
		t.Errorf("overlapping ID 1 (score=%.6f) should outscore vector-only ID 3 (score=%.6f)", scores[1], scores[3])
	}
}

func TestMergeRRFDeduplicates(t *testing.T) {
	shared := []int64{1, 2, 3}
	results := memory.MergeRRF(shared, shared)
	if len(results) != 3 {
		t.Fatalf("expected 3 deduplicated results, got %d", len(results))
	}
}

func TestMergeRRFSortedDescending(t *testing.T) {
	fts := []int64{1, 2, 3, 4, 5}
	vec := []int64{5, 4, 3, 2, 1} // reversed
	results := memory.MergeRRF(fts, vec)

	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted: index %d score %.6f > index %d score %.6f",
				i, results[i].Score, i-1, results[i-1].Score)
		}
	}
}

func TestMergeRRFScoreFormula(t *testing.T) {
	// ID 1 at rank 0 in FTS only. Score = 1/(0+60) = 1/60.
	results := memory.MergeRRF([]int64{1}, nil)
	if len(results) != 1 {
		t.Fatalf("expected 1 result")
	}
	want := 1.0 / 60.0
	if abs(results[0].Score-want) > 1e-9 {
		t.Errorf("score = %.10f, want %.10f", results[0].Score, want)
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
