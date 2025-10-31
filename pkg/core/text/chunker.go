// Package text provides utilities for text processing, such as document chunking.
//
// The functions in this package are designed to be Unicode-aware, ensuring correct
// handling of multi-byte characters. It includes strategies for splitting large
// documents into smaller, manageable pieces for tasks like embedding.
package text

// Chunk represents a single piece of text produced by a chunker.
// It includes the content and its sequential position within the original document.
type Chunk struct {
	Content     string
	ChunkNumber int
}

// FixedSizeChunker splits text into fixed-size chunks with a specified overlap.
//
// This function operates on runes rather than bytes to correctly handle multi-byte
// Unicode characters (e.g., emojis, accented letters), preventing them from being
// split. The overlap helps to maintain semantic context across chunk boundaries,
// which is crucial for downstream tasks like embedding and retrieval.
//
// For example, with a chunkSize of 100 and an overlapSize of 20, the function
// advances by 80 runes for each new chunk. The first chunk would be runes[0:100],
// the second runes[80:180], the third runes[160:260], and so on.
func FixedSizeChunker(text string, chunkSize, overlapSize int) []Chunk {
	if chunkSize <= 0 || overlapSize < 0 || overlapSize >= chunkSize {
		// If the parameters are invalid, return the entire text as a single chunk.
		return []Chunk{{Content: text, ChunkNumber: 0}}
	}

	var chunks []Chunk
	runes := []rune(text) // Use runes to handle Unicode characters correctly.
	length := len(runes)

	if length == 0 {
		return chunks
	}

	chunkNum := 0
	for i := 0; i < length; i += (chunkSize - overlapSize) {
		end := i + chunkSize
		if end > length {
			end = length
		}

		chunks = append(chunks, Chunk{
			Content:     string(runes[i:end]),
			ChunkNumber: chunkNum,
		})

		chunkNum++
	}

	return chunks
}
