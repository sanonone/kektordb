package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
	"github.com/sanonone/kektordb/pkg/rag"
)

// MockEmbedder per evitare di usare Ollama
type MockEmbedder struct{}

func (m *MockEmbedder) Embed(text string) ([]float32, error) {
	// Restituisce un vettore vuoto (non ci interessa la ricerca vettoriale qui, solo i link)
	return make([]float32, 16), nil
}

func main() {
	// Setup Cartelle
	tempDir := "./temp_autolink_test"
	docsDir := filepath.Join(tempDir, "docs")
	dataDir := filepath.Join(tempDir, "data")

	os.RemoveAll(tempDir)
	os.MkdirAll(docsDir, 0755)
	defer os.RemoveAll(tempDir)

	fmt.Println("TEST AUTO-LINKING (RAG Pipeline)")

	// 1. Creiamo un file di testo finto
	// Usiamo separatori chiari per forzare lo split
	content := "ChunkUno.\nChunkDue.\nChunkTre."
	filePath := filepath.Join(docsDir, "story.txt")
	os.WriteFile(filePath, []byte(content), 0644)
	fmt.Println("File creato: story.txt (3 righe)")

	// 2. Avvio Engine
	opts := engine.DefaultOptions(dataDir)
	opts.AutoSaveInterval = 0
	db, err := engine.Open(opts)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// 3. Configurazione Pipeline
	cfg := rag.Config{
		Name:            "link_tester",
		SourcePath:      docsDir,
		IndexName:       "linked_index",
		PollingInterval: 100 * time.Millisecond,

		// --- CONFIGURAZIONE CHIAVE ---
		GraphEnabled: true, // Attiva i link automatici

		// Forziamo lo split per ogni riga
		ChunkingStrategy: "recursive",
		ChunkSize:        10, // Molto piccolo per splittare "ChunkUno."
		ChunkOverlap:     0,
	}

	adapter := rag.NewKektorAdapter(db)
	embedder := &MockEmbedder{}

	// Creazione indice manuale (per sicurezza, anche se la pipeline lo farebbe)
	db.VCreate("linked_index", distance.Cosine, 16, 200, distance.Float32, "", nil, nil, nil)

	pipeline := rag.NewPipeline(cfg, adapter, embedder, nil, nil)

	// 4. Esecuzione
	fmt.Println("⏳ Pipeline avviata...")
	pipeline.Start()

	// Attendiamo ingestione
	time.Sleep(2 * time.Second)
	pipeline.Stop()

	// 5. VERIFICA LINK
	// Gli ID generati sono deterministici: filepath + _index
	// Ci aspettiamo 3 chunk: indices 0, 1, 2

	// Verifichiamo il Chunk Centrale (Indice 1: "ChunkDue.")
	// Deve avere PREV -> 0 e NEXT -> 2
	middleChunkID := fmt.Sprintf("%s_1", filePath)

	fmt.Printf("\nIspeziono Chunk Centrale: %s\n", middleChunkID)

	// A. Controllo PREV
	prevLinks, foundPrev := db.VGetLinks(middleChunkID, "prev")
	if foundPrev && len(prevLinks) > 0 && prevLinks[0] == fmt.Sprintf("%s_0", filePath) {
		fmt.Println("Link PREV trovato correttamente (punta a Chunk 0)")
	} else {
		fmt.Printf("Link PREV fallito. Trovato: %v\n", prevLinks)
	}

	// B. Controllo NEXT
	nextLinks, foundNext := db.VGetLinks(middleChunkID, "next")
	if foundNext && len(nextLinks) > 0 && nextLinks[0] == fmt.Sprintf("%s_2", filePath) {
		fmt.Println("Link NEXT trovato correttamente (punta a Chunk 2)")
	} else {
		fmt.Printf("Link NEXT fallito. Trovato: %v\n", nextLinks)
	}
}
