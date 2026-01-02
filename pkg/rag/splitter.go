package rag

import (
	"strings"
	"unicode/utf8"
)

// Splitter defines the interface for splitting text into chunks
type Splitter interface {
	SplitText(text string) []string
}

// RecursiveCharacterSplitter splits text recursively using a list of separetors.
// It tries to keep related text together (paragraphs, then sentences, then words).
type RecursiveCharacterSplitter struct {
	ChunkSize    int
	ChunkOverlap int
	Separators   []string
}

// NewSplitterFactory creates the appropriate splitter based on configuration.
func NewSplitterFactory(cfg Config) Splitter {
	// Defaults
	size := cfg.ChunkSize
	if size <= 0 {
		size = 500
	}
	overlap := cfg.ChunkOverlap
	if overlap < 0 {
		overlap = 0
	}

	// If the user provided manual separators, use them
	if len(cfg.CustomSeparators) > 0 {
		return &RecursiveCharacterSplitter{
			ChunkSize:    size,
			ChunkOverlap: overlap,
			Separators:   cfg.CustomSeparators,
		}
	}

	// Otherwise, choose based on strategy
	switch cfg.ChunkingStrategy {
	case "code", "go", "python":
		return NewCodeSplitter(size, overlap)
	case "markdown", "md":
		return &RecursiveCharacterSplitter{
			ChunkSize:    size,
			ChunkOverlap: overlap,
			Separators:   []string{"\n## ", "\n### ", "\n\n", "\n", " ", ""}, // Cut on Header
		}
	case "fixed":
		// Simulated fallback using empty separator or simple logic
		// For now recursive with only "" behaves similarly to fixed
		return &RecursiveCharacterSplitter{
			ChunkSize:    size,
			ChunkOverlap: overlap,
			Separators:   []string{""},
		}
	case "recursive":
		fallthrough
	default:
		return NewRecursiveSplitter(size, overlap)
	}
}

// NewRecursiveSplitter creates a splitter with default separators suitable for generic text
func NewRecursiveSplitter(chunkSize, chunkOverlap int) *RecursiveCharacterSplitter {
	if chunkSize <= 0 {
		chunkSize = 500
	}

	return &RecursiveCharacterSplitter{
		ChunkSize:    chunkSize,
		ChunkOverlap: chunkOverlap,
		Separators:   []string{"\n\n", "\n", " ", ""},
	}
}

// NewCodeSplitter creates a splitter optimized for code (Go, Python, etc).
// TODO: implement language-specific separators (e.g., for Ruby, Java) to improve splitting accuracy.
func NewCodeSplitter(chunkSize, chunkOverlap int) *RecursiveCharacterSplitter {
	return &RecursiveCharacterSplitter{
		ChunkSize:    chunkSize,
		ChunkOverlap: chunkOverlap,
		Separators:   []string{"\nfunc", "\ntype", "\nclass", "\nclass", "\n\n", "\n", " ", ""},
	}
}

func (s *RecursiveCharacterSplitter) SplitText(text string) []string {
	finalChunks := []string{}
	goodSplits := s.recursiveSplit(text, s.Separators)

	// Merge small pieces into final chunk respecting size and overlap
	currentDoc := ""
	for _, split := range goodSplits {
		// If adding the next piece exceeds the size...
		if utf8.RuneCountInString(currentDoc)+utf8.RuneCountInString(split) > s.ChunkSize {
			if currentDoc != "" {
				finalChunks = append(finalChunks, currentDoc)

				// Overlap Management: Keep the tail of the previous chunk
				// This is a simplified logic for overlap.
				// For perfect overlap, we should keep a moving window of previous 'splits'.
				// Here we take a "greedy" approach: reset and restart.
				// TODO: Implement sophisticated overlap based on tokens/words.
				currentDoc = ""
			}
		}

		if currentDoc != "" {
			// Add the separator (space) if we are joining words,
			// but be careful: recursiveSplit might have eaten the separators.
			// For simplicity here we concatenate.
			// In real recursive logic, the separator is implicit.
			// This simple splitter assumes pieces are "clean".
		}
		currentDoc += split
	}

	if currentDoc != "" {
		finalChunks = append(finalChunks, currentDoc)
	}

	return finalChunks
}

// recursiveSplit is the core logic. It tries to split 'text' by the first separator.
// If the resulting chunks are too big, it tries the next separator on them.
func (s *RecursiveCharacterSplitter) recursiveSplit(text string, separators []string) []string {
	// finalChunks := []string{}

	// Base case: no separators left, return text (even if large)
	if len(separators) == 0 {
		return []string{text}
	}

	separator := separators[0]
	nextSeparators := separators[1:]

	// Find if the separator exists
	// If separator is empty "", normal char split
	parts := strings.Split(text, separator)

	// If text does not contain separator, move to next level
	// Note: strings.Split returns len 1 slice if sep not found
	if len(parts) == 1 && separator != "" {
		return s.recursiveSplit(text, nextSeparators)
	}

	// Process parts
	var goodSplits []string

	for _, part := range parts {
		if part == "" {
			continue
		}

		// If piece is small, keep it
		if utf8.RuneCountInString(part) < s.ChunkSize {
			goodSplits = append(goodSplits, part)
		} else {
			// If large, recurse with next separators
			if len(nextSeparators) > 0 {
				subSplits := s.recursiveSplit(part, nextSeparators)
				goodSplits = append(goodSplits, subSplits...)
			} else {
				// If no more separators, keep as is (or truncate brutally)
				goodSplits = append(goodSplits, part)
			}
		}
	}

	// Now re-stitch pieces (goodSplits) using current separator
	// to create chunks close to ChunkSize without exceeding it.
	return s.mergeSplits(goodSplits, separator)
}

// mergeSplits combines small pieces using original separator until ChunkSize is reached.
func (s *RecursiveCharacterSplitter) mergeSplits(splits []string, separator string) []string {
	var mergedDocs []string
	var currentDoc []string
	currentLen := 0
	sepLen := utf8.RuneCountInString(separator)

	for _, split := range splits {
		splitLen := utf8.RuneCountInString(split)

		// If adding this piece exceeds limit...
		if currentLen+splitLen+(len(currentDoc)*sepLen) > s.ChunkSize {
			if len(currentDoc) > 0 {
				doc := strings.Join(currentDoc, separator)
				mergedDocs = append(mergedDocs, doc)

				// Overlap Management
				// Keep the tail of the previous chunk that fits in overlap.
				if s.ChunkOverlap > 0 {
					currentDoc = s.removeFirstUntilOverlap(currentDoc, separator, s.ChunkOverlap)

					// Recalculate currentLen for remaining pieces
					currentLen = 0
					for _, p := range currentDoc {
						currentLen += utf8.RuneCountInString(p)
					}
					// Add separators if there are pieces
					if len(currentDoc) > 1 {
						currentLen += (len(currentDoc) - 1) * sepLen
					}
				} else {
					currentDoc = nil
					currentLen = 0
				}
			}
		}

		currentDoc = append(currentDoc, split)
		currentLen += splitLen
	}

	if len(currentDoc) > 0 {
		doc := strings.Join(currentDoc, separator)
		mergedDocs = append(mergedDocs, doc)
	}

	return mergedDocs
}

// removeFirstUntilOverlap removes elements from the head of the slice until the remaining
// combined length (plus separators) is <= overlapSize.
// This is a "best effort" greedy approach for overlap.
func (s *RecursiveCharacterSplitter) removeFirstUntilOverlap(parts []string, separator string, overlapSize int) []string {
	sepLen := utf8.RuneCountInString(separator)
	totalLen := 0
	for _, p := range parts {
		totalLen += utf8.RuneCountInString(p)
	}
	// Add separators length
	if len(parts) > 1 {
		totalLen += (len(parts) - 1) * sepLen
	}

	// Remove from front until we fit in overlapSize
	newParts := parts
	for len(newParts) > 0 && totalLen > overlapSize {
		removed := newParts[0]
		newParts = newParts[1:]

		removedLen := utf8.RuneCountInString(removed)
		totalLen -= removedLen
		// If there are still parts, we removed a separator too
		if len(newParts) > 0 {
			totalLen -= sepLen
		}
	}
	return newParts
}
