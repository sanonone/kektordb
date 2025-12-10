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
// It try to keep related text together (paragraphs, then sentences, then words).
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

	// Se l'utente ha fornito separatori manuali, usiamo quelli
	if len(cfg.CustomSeparators) > 0 {
		return &RecursiveCharacterSplitter{
			ChunkSize:    size,
			ChunkOverlap: overlap,
			Separators:   cfg.CustomSeparators,
		}
	}

	// Altrimenti, scegliamo in base alla strategia
	switch cfg.ChunkingStrategy {
	case "code", "go", "python":
		return NewCodeSplitter(size, overlap)
	case "markdown", "md":
		return &RecursiveCharacterSplitter{
			ChunkSize:    size,
			ChunkOverlap: overlap,
			Separators:   []string{"\n## ", "\n### ", "\n\n", "\n", " ", ""}, // Taglia su Header
		}
	case "fixed":
		// Fallback simulato usando separatore vuoto o logica semplice
		// Per ora il recursive con solo "" si comporta similmente al fixed
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
// TODO: improvement add specific separators for languagfes
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

	// Merge dei piccoli pezzi nel chunk finale rispettando la size e l'overlap
	currentDoc := ""
	for _, split := range goodSplits {
		// Se aggiungere il prossimo pezzo supera la dimensione...
		if utf8.RuneCountInString(currentDoc)+utf8.RuneCountInString(split) > s.ChunkSize {
			if currentDoc != "" {
				finalChunks = append(finalChunks, currentDoc)

				// Gestione Overlap: Manteniamo la coda del chunk precedente
				// Questa è una logica semplificata per l'overlap.
				// Per un overlap perfetto, dovremmo tenere una finestra mobile dei 'split' precedenti.
				// Qui facciamo un approccio "greedy": resettiamo e ripartiamo.
				// TODO: Implementare overlap sofisticato basato su token/parole.
				currentDoc = ""
			}
		}

		if currentDoc != "" {
			// Aggiungiamo il separatore (spazio) se stiamo unendo parole,
			// ma attenzione: recursiveSplit potrebbe aver mangiato i separatori.
			// Per semplicità qui concateniamo.
			// Nella logica ricorsiva reale, il separatore è implicito.
			// Questo splitter semplice assume che i pezzi siano "puliti".
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

	// Caso base: nessun separatore rimasto, restituiamo il testo (anche se grande)
	if len(separators) == 0 {
		return []string{text}
	}

	separator := separators[0]
	nextSeparators := separators[1:]

	// Troviamo se il separatore esiste
	// Se il separatore è vuoto "", split normale per caratteri
	parts := strings.Split(text, separator)

	// Se il testo non contiene il separatore, passiamo al prossimo livello
	// Nota: strings.Split restituisce slice len 1 se sep non trovato
	if len(parts) == 1 && separator != "" {
		return s.recursiveSplit(text, nextSeparators)
	}

	// Processiamo le parti
	var goodSplits []string

	for _, part := range parts {
		if part == "" {
			continue
		}

		// Se il pezzo è piccolo, lo teniamo
		if utf8.RuneCountInString(part) < s.ChunkSize {
			goodSplits = append(goodSplits, part)
		} else {
			// Se è grande, ricorsione con i separatori successivi
			if len(nextSeparators) > 0 {
				subSplits := s.recursiveSplit(part, nextSeparators)
				goodSplits = append(goodSplits, subSplits...)
			} else {
				// Se non ci sono più separatori, dobbiamo tenerlo così com'è (o troncarlo brutalmente)
				goodSplits = append(goodSplits, part)
			}
		}
	}

	// Ora dobbiamo "ricucire" i pezzi (goodSplits) usando il separatore corrente
	// per creare chunk che si avvicinano a ChunkSize senza superarlo.
	return s.mergeSplits(goodSplits, separator)
}

// mergeSplits combina piccoli pezzi usando il separatore originale finché non raggiungono ChunkSize.
func (s *RecursiveCharacterSplitter) mergeSplits(splits []string, separator string) []string {
	var mergedDocs []string
	var currentDoc []string
	currentLen := 0
	sepLen := utf8.RuneCountInString(separator)

	for _, split := range splits {
		splitLen := utf8.RuneCountInString(split)

		// Se aggiungere questo pezzo supera il limite...
		if currentLen+splitLen+(len(currentDoc)*sepLen) > s.ChunkSize {
			if len(currentDoc) > 0 {
				doc := strings.Join(currentDoc, separator)
				mergedDocs = append(mergedDocs, doc)

				// Gestione Overlap
				// Manteniamo la coda del chunk precedente che rientra nell'overlap.
				if s.ChunkOverlap > 0 {
					currentDoc = s.removeFirstUntilOverlap(currentDoc, separator, s.ChunkOverlap)

					// Ricalcoliamo currentLen per i pezzi rimasti
					currentLen = 0
					for _, p := range currentDoc {
						currentLen += utf8.RuneCountInString(p)
					}
					// Aggiungiamo i separatori se ci sono pezzi
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
