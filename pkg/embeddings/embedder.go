package embeddings

// Embedder defines the interface for converting text into vector representations.
type Embedder interface {
	Embed(text string) ([]float32, error)
	// EmbedBatch converts a slice of texts into vectors in the same order.
	// Implementations should use the provider's native batch API when available
	// (single round trip for all texts). If the batch endpoint is unavailable,
	// implementations MUST fall back to a serial loop of Embed and return
	// successful results — never fail a batch solely because batching is unsupported.
	EmbedBatch(texts []string) ([][]float32, error)
}

// embedBatchSerial is the default fallback: embed each text one at a time.
// Used by backends without a native batch path (e.g. NoopEmbedder) and as
// the fallback inside batch implementations when the provider rejects batch.
func embedBatchSerial(e Embedder, texts []string) ([][]float32, error) {
	vecs := make([][]float32, len(texts))
	for i, text := range texts {
		vec, err := e.Embed(text)
		if err != nil {
			return nil, err
		}
		vecs[i] = vec
	}
	return vecs, nil
}
