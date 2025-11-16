package hnsw

import (
	"fmt"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/types"
	"math/rand"
	"os"
	"runtime/pprof"
	"sync/atomic"
	"testing"
)

// generateTestObjects crea un set di dati di test con vettori casuali.
// Viene usata sia per i benchmark che per i test di profiling per garantire
// coerenza e ridurre la duplicazione del codice.
func generateTestObjects(numVectors int, vectorDim int) []types.BatchObject {
	// Usiamo un seme fisso per rendere i test deterministici, se necessario.
	// Per i benchmark di performance pura, un seme variabile va bene.
	rng := rand.New(rand.NewSource(42))

	objects := make([]types.BatchObject, numVectors)
	for i := 0; i < numVectors; i++ {
		vec := make([]float32, vectorDim)
		for j := 0; j < vectorDim; j++ {
			vec[j] = rng.Float32()
		}
		objects[i] = types.BatchObject{
			// L'allocazione della stringa avviene qui, una sola volta per tutti i test.
			Id:     fmt.Sprintf("vec-%d", i),
			Vector: vec,
		}
	}
	return objects
}

// BenchmarkConcurrentInserts misura le performance dell'inserimento di vettori
// in parallelo. Questo è un test cruciale per identificare i colli di bottiglia
// dovuti alla contesa sui lock in scenari multi-threaded.
func BenchmarkConcurrentInserts(b *testing.B) {
	// --- Setup ---
	// Prepariamo in anticipo un set di dati sufficientemente grande da non
	// essere banale. La dimensione del vettore è comune (es. modelli MiniLM).
	const (
		numVectors = 10000 // Numero di vettori da inserire nel test
		vectorDim  = 384   // Dimensione comune per embedding
	)

	// Creiamo i vettori una sola volta, fuori dal ciclo di benchmark,
	// per non misurare il costo della loro generazione.
	vectors := make([][]float32, numVectors)
	for i := 0; i < numVectors; i++ {
		vec := make([]float32, vectorDim)
		for j := 0; j < vectorDim; j++ {
			vec[j] = rand.Float32()
		}
		vectors[i] = vec
	}

	// --- Benchmark ---
	b.Run("ConcurrentAdd", func(b *testing.B) {
		// Creiamo un nuovo indice per ogni esecuzione del benchmark per partire
		// da uno stato pulito.
		idx, _ := New(16, 200, distance.Cosine, distance.Float32, "")

		// Iniziamo a misurare il tempo solo da qui, dopo tutto il setup.
		b.ResetTimer()

		// Usiamo un contatore atomico per distribuire il lavoro (i vettori da inserire)
		// tra le varie goroutine create da RunParallel. Questo evita la necessità
		// di un lock per accedere alla slice dei vettori.
		var counter uint32

		// Esegue il codice del body in parallelo su più goroutine.
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				// Ottiene un indice univoco in modo atomico.
				currentIndex := atomic.AddUint32(&counter, 1) - 1

				// Assicuriamoci di non andare oltre la nostra slice di vettori pre-generati.
				if int(currentIndex) >= len(vectors) {
					continue
				}

				vectorID := fmt.Sprintf("vec-%d", currentIndex)
				vector := vectors[currentIndex]

				// Questa è l'operazione che stiamo misurando.
				// Attualmente, si prevede che questa chiamata causi una forte contesa
				// sul lock globale dell'indice.
				_, err := idx.Add(vectorID, vector)
				if err != nil {
					b.Errorf("Errore durante l'inserimento concorrente: %v", err)
				}
			}
		})
	})
}

// --- SOSTITUIRE il test di profiling CON QUESTA VERSIONE ---
func TestLargeBatchInsertionForProfiling(t *testing.T) {
	// --- Setup: la generazione dei dati avviene QUI, fuori dalla profilazione ---
	const (
		totalVectors = 100000
		vectorDim    = 100
	)
	objects := generateTestObjects(totalVectors, vectorDim)

	// --- Inizio della profilazione ---
	cpuFile, err := os.Create("cpu.pprof")
	if err != nil {
		t.Fatal(err)
	}
	if err := pprof.StartCPUProfile(cpuFile); err != nil {
		t.Fatalf("could not start CPU profile: %v", err)
	}
	defer pprof.StopCPUProfile()

	// L'unica cosa che misuriamo è la creazione e l'inserimento.
	idx, _ := New(16, 200, distance.Cosine, distance.Float32, "")
	err = idx.AddBatch(objects)
	if err != nil {
		t.Fatal(err)
	}

	// Profiling della memoria
	memFile, err := os.Create("mem.pprof")
	if err != nil {
		t.Fatal(err)
	}
	defer memFile.Close()
	if err := pprof.WriteHeapProfile(memFile); err != nil {
		t.Fatalf("could not write heap profile: %v", err)
	}
}

// --- SOSTITUIRE il benchmark CON QUESTA VERSIONE ---
func BenchmarkConcurrentAddBatch(b *testing.B) {
	// --- Setup Unico (fuori dal loop di benchmark) ---
	const (
		totalVectors = 100000
		vectorDim    = 100
	)
	// I dati di test, incluse le stringhe ID, vengono generati una sola volta.
	objects := generateTestObjects(totalVectors, vectorDim)

	// --- Inizio del Benchmark ---
	b.ReportAllocs()
	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		// Stoppiamo il timer solo per la creazione dell'indice pulito.
		b.StopTimer()
		idx, _ := New(16, 200, distance.Cosine, distance.Float32, "")
		b.StartTimer()

		// --- Operazione da Misurare ---
		err := idx.AddBatch(objects)
		if err != nil {
			b.Fatalf("AddBatch fallito: %v", err)
		}
	}
}
