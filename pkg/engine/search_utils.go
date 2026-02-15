package engine

import (
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/sanonone/kektordb/pkg/core/types"
)

// fusedResult is a helper struct for sorting combined results.
type fusedResult struct {
	id    string // External ID
	score float64
}

var containsRegex = regexp.MustCompile(`(?si)\s*CONTAINS\s*\(\s*(\w+)\s*,\s*['"](.+?)['"]\s*\)`)

// parseHybridFilter separates the text filter (CONTAINS) from the boolean filters.
func parseHybridFilter(filter string) (booleanFilter, textQuery, textField string) {
	matches := containsRegex.FindStringSubmatch(filter)

	if len(matches) == 0 {
		// No CONTAINS clause, the entire filter is boolean.
		return filter, "", ""
	}

	// Extract parts: matches[1] is field, matches[2] is query
	textField = matches[1]
	textQuery = matches[2]

	// Remove the CONTAINS clause from the original filter string
	booleanFilter = strings.Replace(filter, matches[0], "", 1)

	// Cleanup AND/OR leftovers
	booleanFilter = strings.TrimSpace(booleanFilter)
	booleanFilter = strings.TrimPrefix(booleanFilter, "AND ")
	booleanFilter = strings.TrimSuffix(booleanFilter, " AND")
	booleanFilter = strings.TrimSpace(booleanFilter)

	return booleanFilter, textQuery, textField
}

// normalizeVectorScores maps distance (lower is better) to score (0..1, higher is better).
// Logic: 1 / (1 + distance)
// REVERTED from MinMax to maintain absolute score stability for RAG thresholds.
func normalizeVectorScores(results []types.SearchResult) {
	for i := range results {
		results[i].Score = 1.0 / (1.0 + results[i].Score)
	}
}

// normalizeTextScores maps BM25 scores to 0..1 range based on the max score in the batch.
func normalizeTextScores(results []types.SearchResult) {
	if len(results) == 0 {
		return
	}
	maxScore := 0.0
	for _, res := range results {
		if res.Score > maxScore {
			maxScore = res.Score
		}
	}
	if maxScore > 0 {
		for i := range results {
			results[i].Score /= maxScore
		}
	}
}

// calculateTimeDecay computes the decay factor based on age and half-life.
// Formula: 2^(-age/halfLife)
func calculateTimeDecay(createdAt float64, halfLifeSeconds float64) float64 {
	now := float64(time.Now().Unix())
	age := now - createdAt

	if age <= 0 {
		return 1.0 // Created just now (or in the future due to clock skew)
	}
	if halfLifeSeconds <= 0 {
		return 1.0 // Decay disabled
	}

	// Calculate decay
	// Example: Age = 7 days, HalfLife = 7 days -> 2^-1 = 0.5
	return math.Pow(2, -age/halfLifeSeconds)
}
