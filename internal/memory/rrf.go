package memory

import "sort"

const rrfK = 60

// RRFResult is a document candidate used during rank fusion.
type RRFResult struct {
	ID      int64
	RankFTS int // 0-based rank from FTS5 results; -1 = absent
	RankVec int // 0-based rank from vector results; -1 = absent
	Score   float64
}

// MergeRRF merges two ranked lists of document IDs using Reciprocal Rank
// Fusion and returns the fused list ordered by descending score.
//
// ftsIDs and vecIDs are ordered slices of document IDs (most relevant first).
func MergeRRF(ftsIDs, vecIDs []int64) []RRFResult {
	scores := make(map[int64]*RRFResult)

	for rank, id := range ftsIDs {
		r := getOrCreate(scores, id)
		r.RankFTS = rank
		r.Score += 1.0 / float64(rank+rrfK)
	}
	for rank, id := range vecIDs {
		r := getOrCreate(scores, id)
		r.RankVec = rank
		r.Score += 1.0 / float64(rank+rrfK)
	}

	results := make([]RRFResult, 0, len(scores))
	for _, r := range scores {
		results = append(results, *r)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results
}

func getOrCreate(m map[int64]*RRFResult, id int64) *RRFResult {
	if r, ok := m[id]; ok {
		return r
	}
	r := &RRFResult{ID: id, RankFTS: -1, RankVec: -1}
	m[id] = r
	return r
}
