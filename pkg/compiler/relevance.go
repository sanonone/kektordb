package compiler

import (
	"math"
	"sort"
	"strings"
	"time"
)

// FilterByRelevance scores and filters source nodes by relevance
// to the task specification, returning only nodes above the threshold
// ordered by descending score.
func (c *Compiler) FilterByRelevance(
	nodes []NodeInfo,
	template *CompileTemplate,
	taskSpec *TaskSpec,
) []NodeInfo {
	threshold := 0.1 // low base threshold for deterministic compilation
	if taskSpec != nil && hasRelevantKeywords(taskSpec.Description) {
		threshold = 0.2 // slightly higher for keyword-based tasks
	}

	var relevant []NodeInfo
	for _, node := range nodes {
		score := c.computeRelevance(node, template, taskSpec)
		if score >= threshold {
			node.Score = score
			relevant = append(relevant, node)
		}
	}

	sort.Slice(relevant, func(i, j int) bool {
		return relevant[i].Score > relevant[j].Score
	})

	if len(relevant) > 50 {
		relevant = relevant[:50]
	}

	return relevant
}

func (c *Compiler) computeRelevance(
	node NodeInfo,
	template *CompileTemplate,
	taskSpec *TaskSpec,
) float64 {
	score := 0.0

	// Entity type match
	if template != nil {
		if len(template.EntityTypes) == 0 {
			// Template matches any entity type
			score += 0.15
		} else {
			for _, et := range template.EntityTypes {
				if v, ok := node.Metadata["type"]; ok {
					if s, ok := v.(string); ok && s == et {
						score += 0.3
						break
					}
				}
			}
		}
	}

	// Keyword match from task description
	if taskSpec != nil && taskSpec.Description != "" {
		taskWords := tokenize(strings.ToLower(taskSpec.Description))
		content := strings.ToLower(node.Content)
		matchCount := 0
		for _, w := range taskWords {
			if strings.Contains(content, w) {
				matchCount++
			}
		}
		if len(taskWords) > 0 {
			score += 0.4 * (float64(matchCount) / float64(len(taskWords)))
		}
	}

	// Recency bonus (exponential decay, 7-day half-life)
	if !node.CreatedAt.IsZero() {
		hoursAgo := time.Since(node.CreatedAt).Hours()
		recencyBonus := math.Exp(-hoursAgo / 168.0)
		score += 0.3 * recencyBonus
	}

	// Pinned node bonus
	if node.IsPinned {
		score += 0.2
	}

	return math.Min(score, 1.0)
}

func tokenize(text string) []string {
	fields := strings.Fields(text)
	stopwords := map[string]bool{
		"the": true, "is": true, "at": true, "which": true, "on": true,
		"a": true, "an": true, "of": true, "to": true, "in": true,
		"for": true, "and": true, "or": true, "it": true, "be": true,
	}
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if !stopwords[f] {
			out = append(out, f)
		}
	}
	return out
}

func hasRelevantKeywords(description string) bool {
	return len(tokenize(description)) > 0
}
