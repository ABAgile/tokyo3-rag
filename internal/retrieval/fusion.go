package retrieval

import (
	"sort"

	"github.com/abagile/tokyo3-rag/internal/model"
)

const rrfK = 60

// Result is a single ranked retrieval result. Either DocChunk or CodeNode is set.
type Result struct {
	Score    float64
	DocChunk *model.DocChunk
	CodeNode *model.CodeNode
}

func (r Result) id() string {
	if r.DocChunk != nil {
		return "doc:" + r.DocChunk.ID
	}
	if r.CodeNode != nil {
		return "code:" + r.CodeNode.ID
	}
	return ""
}

// RRF merges multiple ranked result lists using Reciprocal Rank Fusion (k=60).
func RRF(lists ...[]Result) []Result {
	scores := map[string]float64{}
	items := map[string]Result{}

	for _, list := range lists {
		for rank, item := range list {
			id := item.id()
			if id == "" {
				continue
			}
			scores[id] += 1.0 / float64(rrfK+rank+1)
			if _, ok := items[id]; !ok {
				items[id] = item
			}
		}
	}

	merged := make([]Result, 0, len(scores))
	for id, score := range scores {
		r := items[id]
		r.Score = score
		merged = append(merged, r)
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score > merged[j].Score
	})
	return merged
}
