package mcp

import "fmt"

// embedForStore embeds text and guarantees a usable vector.
//
// The embedder implementations now reject an empty result, but the interface
// does not promise it and a custom Embedder can still return (nil, nil). An
// empty vector reaching VAdd is worse than an error: the index accepts it when
// the dimension is already known, storing a zero vector that cosine distance
// cannot rank. The memory looks saved and is silently unreachable.
//
// Every path that turns text into a stored vector goes through here.
func (s *Service) embedForStore(text string) ([]float32, error) {
	vec, err := s.embedder.Embed(text)
	if err != nil {
		return nil, err
	}
	if len(vec) == 0 {
		return nil, fmt.Errorf("embedder returned an empty vector for this text")
	}
	return vec, nil
}

// embedForQuery is the query-side equivalent.
//
// A zero query vector does not corrupt anything, but it makes the search
// meaningless: every node sits at the same distance. Better to fail and say so
// than to return arbitrary memories.
func (s *Service) embedForQuery(text string) ([]float32, error) {
	vec, err := s.embedder.Embed(text)
	if err != nil {
		return nil, err
	}
	if len(vec) == 0 {
		return nil, fmt.Errorf("embedder returned an empty vector for this query")
	}
	return vec, nil
}
