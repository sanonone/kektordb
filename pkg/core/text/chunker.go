package text

// Chunk rappresenta un singolo pezzo di testo.
type Chunk struct {
	Content     string
	ChunkNumber int
}

/*
Prende il testo, la dimensione del chunk e la dimensione della sovrapposizione come input.

Lavora con le rune invece che con i byte per non tagliare a metà i caratteri multi-byte (come emoji o lettere accentate).

Avanza nel testo di chunkSize - overlapSize. Ad esempio, con chunkSize=100 e overlap=20, il primo chunk sarà [0:100], il secondo [80:180], il terzo [160:260], e così via.

La sovrapposizione di 20 caratteri assicura che il contesto non venga perso bruscamente tra un chunk e l'altro.
*/
// FixedSizeChunker divide il testo in chunk di dimensione fissa con sovrapposizione.
// La sovrapposizione aiuta a mantenere il contesto tra i chunk.
func FixedSizeChunker(text string, chunkSize, overlapSize int) []Chunk {
	if chunkSize <= 0 || overlapSize < 0 || overlapSize >= chunkSize {
		// Se i parametri non sono validi, restituisci l'intero testo come un unico chunk.
		return []Chunk{{Content: text, ChunkNumber: 0}}
	}

	var chunks []Chunk
	runes := []rune(text) // rune per gestire correttamente i caratteri Unicode
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
