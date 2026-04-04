package cognitive

import (
	"context"
	"fmt"

	"github.com/sanonone/kektordb/pkg/client"
)

// RetrievalStrategy defines the context assembly strategy.
type RetrievalStrategy string

const (
	// StrategyGreedy prioritizes high-relevance chunks first.
	StrategyGreedy RetrievalStrategy = "greedy"
	// StrategyDensity maximizes information density.
	StrategyDensity RetrievalStrategy = "density"
	// StrategyGraph uses graph relationships for expansion.
	StrategyGraph RetrievalStrategy = "graph"
)

// ContextAssembler provides intelligent context assembly for RAG applications.
type ContextAssembler struct {
	client         *client.Client
	strategy       RetrievalStrategy
	maxTokens      int
	expansionDepth int
	semanticWeight float64
	graphWeight    float64
	densityWeight  float64
	charsPerToken  float64
}

// ContextAssemblerOptions configures the assembler.
type ContextAssemblerOptions struct {
	Strategy       RetrievalStrategy
	MaxTokens      int
	ExpansionDepth int
	SemanticWeight float64
	GraphWeight    float64
	DensityWeight  float64
	CharsPerToken  float64
}

// NewContextAssembler creates a context assembler with sensible defaults.
func NewContextAssembler(c *client.Client, opts *ContextAssemblerOptions) *ContextAssembler {
	ca := &ContextAssembler{
		client:         c,
		strategy:       StrategyGraph,
		maxTokens:      4000,
		expansionDepth: 2,
		semanticWeight: 0.4,
		graphWeight:    0.3,
		densityWeight:  0.3,
		charsPerToken:  4.0,
	}

	if opts != nil {
		if opts.Strategy != "" {
			ca.strategy = opts.Strategy
		}
		if opts.MaxTokens > 0 {
			ca.maxTokens = opts.MaxTokens
		}
		if opts.ExpansionDepth > 0 {
			ca.expansionDepth = opts.ExpansionDepth
		}
		if opts.SemanticWeight > 0 {
			ca.semanticWeight = opts.SemanticWeight
		}
		if opts.GraphWeight > 0 {
			ca.graphWeight = opts.GraphWeight
		}
		if opts.DensityWeight > 0 {
			ca.densityWeight = opts.DensityWeight
		}
		if opts.CharsPerToken > 0 {
			ca.charsPerToken = opts.CharsPerToken
		}
	}

	return ca
}

// Retrieve performs adaptive context retrieval.
func (ca *ContextAssembler) Retrieve(pipelineName, query string, k int) (*client.AdaptiveRetrieveResponse, error) {
	req := client.AdaptiveRetrieveRequest{
		PipelineName:      pipelineName,
		Query:             query,
		K:                 k,
		MaxTokens:         ca.maxTokens,
		Strategy:          string(ca.strategy),
		ExpansionDepth:    ca.expansionDepth,
		SemanticWeight:    ca.semanticWeight,
		GraphWeight:       ca.graphWeight,
		DensityWeight:     ca.densityWeight,
		CharsPerToken:     ca.charsPerToken,
		IncludeProvenance: true,
	}

	return ca.client.AdaptiveRetrieve(req)
}

// RetrieveWithContext performs retrieval within a session context.
func (ca *ContextAssembler) RetrieveWithContext(session *Session, pipelineName, query string, k int) (*client.AdaptiveRetrieveResponse, error) {
	// Get session context for potential query enhancement
	ctx := session.GetContext()
	_ = ctx // Could be used for query expansion in the future

	req := client.AdaptiveRetrieveRequest{
		PipelineName:      pipelineName,
		Query:             query,
		K:                 k,
		MaxTokens:         ca.maxTokens,
		Strategy:          string(ca.strategy),
		ExpansionDepth:    ca.expansionDepth,
		SemanticWeight:    ca.semanticWeight,
		GraphWeight:       ca.graphWeight,
		DensityWeight:     ca.densityWeight,
		CharsPerToken:     ca.charsPerToken,
		IncludeProvenance: true,
	}

	resp, err := ca.client.AdaptiveRetrieve(req)
	if err != nil {
		return nil, err
	}

	// Add interaction to session context
	session.AddMessage("user", query)
	if len(resp.ContextText) > 0 {
		session.AddMessage("system", fmt.Sprintf("Retrieved %d chunks", resp.ChunksUsed))
	}

	return resp, nil
}

// RetrieveWithCancel performs retrieval with context cancellation support.
func (ca *ContextAssembler) RetrieveWithCancel(ctx context.Context, pipelineName, query string, k int) (*client.AdaptiveRetrieveResponse, error) {
	type result struct {
		resp *client.AdaptiveRetrieveResponse
		err  error
	}

	done := make(chan result, 1)
	go func() {
		resp, err := ca.Retrieve(pipelineName, query, k)
		done <- result{resp, err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-done:
		return r.resp, r.err
	}
}

// FormatSources formats source attributions for display.
func FormatSources(sources []client.SourceAttribution) string {
	if len(sources) == 0 {
		return "No sources available"
	}

	var result string
	for i, src := range sources {
		result += fmt.Sprintf("[%d] %s (relevance: %.2f)\n", i+1, src.Filename, src.Relevance)
		if src.GraphPath.Formatted != "" {
			result += fmt.Sprintf("    Path: %s\n", src.GraphPath.Formatted)
		}
		if len(src.Content) > 100 {
			result += fmt.Sprintf("    Content: %s...\n", src.Content[:100])
		} else {
			result += fmt.Sprintf("    Content: %s\n", src.Content)
		}
	}
	return result
}

// SourceFilter provides utilities for filtering sources.
type SourceFilter struct {
	MinRelevance float64
	MaxDepth     int
	VerifiedOnly bool
}

// FilterSources filters sources based on criteria.
func FilterSources(sources []client.SourceAttribution, filter SourceFilter) []client.SourceAttribution {
	var filtered []client.SourceAttribution
	for _, src := range sources {
		if src.Relevance < filter.MinRelevance {
			continue
		}
		if filter.MaxDepth > 0 && src.GraphDepth > filter.MaxDepth {
			continue
		}
		if filter.VerifiedOnly && !src.Verified {
			continue
		}
		filtered = append(filtered, src)
	}
	return filtered
}

// GroupSourcesByDocument groups sources by their parent document.
func GroupSourcesByDocument(sources []client.SourceAttribution) map[string][]client.SourceAttribution {
	groups := make(map[string][]client.SourceAttribution)
	for _, src := range sources {
		docID := src.DocumentID
		if docID == "" {
			docID = "unknown"
		}
		groups[docID] = append(groups[docID], src)
	}
	return groups
}
