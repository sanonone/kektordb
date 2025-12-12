package embeddings

// Embedder defines the interface for converting text into vector representations.
type Embedder interface {
	Embed(text string) ([]float32, error)
	// TODO: EmbedBatch(texts []string)
}
