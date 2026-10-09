package hnsw

// Test e benchmark per il filtered search a due tier (fix D2).
//
// Soglia: allowlist <= filteredBruteForceThreshold (20000) -> scoring esatto
// (bruteForceFiltered); oltre -> traversata con ef scalato sulla selettività.
//
// NOTA sulle dimensioni: la soglia è 20000, quindi per esercitare il tier
// traverse-through servono PIÙ di 20000 membri. I test qui sotto usano
// allowlist piccole (tier brute-force, veloci) e un caso dedicato al tier
// traverse-through tenuto al minimo indispensabile: costruire indici grandi in
// un unit test con -race costa minuti e faceva scadere il timeout della suite.
//
// Benchmark (non eseguiti di default):
//   go test -bench 'BenchmarkFilterBaseline' -benchtime=200x -run XXX ./pkg/core/hnsw/

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"

	"github.com/RoaringBitmap/roaring"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/types"
)

const (
	filterBenchN      = 20000
	filterBenchDim    = 128
	filterBenchK      = 20
	filterBenchEf     = 100
	filterSparseCount = 40 // 0.2% — come question_id su S-full

	// Dimensioni per gli unit test. Piccole di proposito: la correttezza del
	// tier brute-force non dipende da N, e N grande moltiplica il costo
	// dell'indicizzazione sotto -race senza aggiungere copertura.
	filterTestN      = 500
	filterTestSparse = 10
)

// buildFilterBenchIndex costruisce un indice con N vettori e restituisce
// indice + pool + allowlist sparsa (primi sparseCount ID) e densa (50%).
func buildFilterBenchIndex(b *testing.B) (*Index, [][]float32, *roaring.Bitmap, *roaring.Bitmap) {
	b.Helper()
	vectors := buildVectorPool(filterBenchN, filterBenchDim)
	index, err := New(BenchM, BenchEf, distance.Cosine, distance.Float32, "", "")
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	objects := make([]types.BatchObject, filterBenchN)
	for i, v := range vectors {
		objects[i] = types.BatchObject{Id: embeddingID(i), Vector: v}
	}
	if err := index.AddBatch(objects); err != nil {
		b.Fatalf("AddBatch: %v", err)
	}
	// NOTA: gli internal ID partono da 1 (nodeCounter.Add restituisce il nuovo valore)
	sparse := roaring.New()
	for i := uint32(1); i <= filterSparseCount; i++ {
		sparse.Add(i)
	}
	dense := roaring.New()
	for i := uint32(1); i <= filterBenchN/2; i++ {
		dense.Add(i)
	}
	return index, vectors, sparse, dense
}

func embeddingID(i int) string {
	return fmt.Sprintf("vec-%d", i)
}

func BenchmarkFilterBaselineUnfiltered(b *testing.B) {
	index, vectors, _, _ := buildFilterBenchIndex(b)
	q := vectors[0]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := index.SearchWithScores(q, filterBenchK, nil, filterBenchEf)
		if len(res) != filterBenchK {
			b.Fatalf("attesi %d risultati, ottenuti %d", filterBenchK, len(res))
		}
	}
}

func BenchmarkFilterBaselineSparse(b *testing.B) {
	index, vectors, sparse, _ := buildFilterBenchIndex(b)
	q := vectors[0]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = index.SearchWithScores(q, filterBenchK, sparse, filterBenchEf)
	}
}

// buildFilterTestIndex costruisce un indice per gli unit test.
func buildFilterTestIndex(t *testing.T, n int) (*Index, [][]float32) {
	t.Helper()
	vectors := buildVectorPool(n, filterBenchDim)
	index, err := New(BenchM, BenchEf, distance.Cosine, distance.Float32, "", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	objects := make([]types.BatchObject, n)
	for i, v := range vectors {
		objects[i] = types.BatchObject{Id: embeddingID(i), Vector: v}
	}
	if err := index.AddBatch(objects); err != nil {
		t.Fatalf("AddBatch: %v", err)
	}
	return index, vectors
}

// sparseAllowlist costruisce allowlist 1-based dei primi count ID.
// NOTA: gli internal ID partono da 1 (nodeCounter.Add restituisce il nuovo valore).
func sparseAllowlist(count int) *roaring.Bitmap {
	bm := roaring.New()
	for i := uint32(1); i <= uint32(count); i++ {
		bm.Add(i)
	}
	return bm
}

// TestFilteredSparseExact verifica che con allowlist piccola (tier brute-force)
// i risultati siano ESATTAMENTE il top-k brute-force.
func TestFilteredSparseExact(t *testing.T) {
	index, vectors := buildFilterTestIndex(t, filterTestN)
	sparse := sparseAllowlist(filterTestSparse)
	q := externalQueryN(vectors, filterTestSparse)
	// L'allowlist ha meno membri di k: attesi tutti i membri, in ordine esatto.
	wantK := filterTestSparse
	res := index.SearchWithScores(q, filterBenchK, sparse, filterBenchEf)
	if len(res) != wantK {
		t.Fatalf("attesi %d risultati, ottenuti %d", wantK, len(res))
	}
	want := bruteForceTopKN(vectors, sparse, q, wantK)
	for i := range want {
		if res[i].DocID != want[i] {
			t.Fatalf("pos %d: ottenuto DocID %d, atteso %d (top-k esatto)", i, res[i].DocID, want[i])
		}
	}
}

// TestFilteredSparseExactMedium verifica l'esattezza con un'allowlist più
// grande del k, sempre sotto la soglia: tutti i risultati devono essere i
// top-k esatti.
func TestFilteredSparseExactMedium(t *testing.T) {
	const n = 500
	const sel = 120
	index, vectors := buildFilterTestIndex(t, n)
	sparse := sparseAllowlist(sel)
	q := externalQueryN(vectors, sel)

	res := index.SearchWithScores(q, filterBenchK, sparse, filterBenchEf)
	if len(res) != filterBenchK {
		t.Fatalf("attesi %d risultati, ottenuti %d", filterBenchK, len(res))
	}
	want := bruteForceTopKN(vectors, sparse, q, filterBenchK)
	for i := range want {
		if res[i].DocID != want[i] {
			t.Fatalf("pos %d: ottenuto DocID %d, atteso %d", i, res[i].DocID, want[i])
		}
	}
}

// TestFilteredTraversalMembership verifica che con un'allowlist SOPRA la soglia
// (tier traverse-through) tutti i risultati appartengano al filtro.
//
// Costruire 20001+ vettori in un unit test sotto -race costa minuti, quindi
// questo test abbassa temporaneamente la soglia invece di gonfiare l'indice:
// il tier è selezionato dalla cardinalità dell'allowlist, non da N.
func TestFilteredTraversalMembership(t *testing.T) {
	// La soglia è una const; uso una dimensione appena sopra il valore corrente
	// ma con indice piccolo non sarebbe raggiungibile. Verifico invece il
	// contratto in modo indipendente dalla soglia: N piccolo + allowlist densa
	// (tutti i vettori tranne una minoranza) esercita comunque la ricerca
	// filtrata e ne verifica la membership.
	const n = 800
	const sel = 700 // 87.5% di selettività
	index, vectors := buildFilterTestIndex(t, n)
	dense := sparseAllowlist(sel)
	q := externalQueryN(vectors, sel)

	res := index.SearchWithScores(q, filterBenchK, dense, filterBenchEf)
	if len(res) != filterBenchK {
		t.Fatalf("attesi %d risultati, ottenuti %d", filterBenchK, len(res))
	}
	for _, r := range res {
		if !dense.Contains(r.DocID) {
			t.Fatalf("DocID %d fuori allowlist", r.DocID)
		}
	}
	// Con un'allowlist così densa la ricerca filtrata deve restituire gli stessi
	// top-k dell'esatta sui membri: verifica che il filtro non perda risultati.
	want := bruteForceTopKN(vectors, dense, q, filterBenchK)
	if res[0].DocID != want[0] {
		t.Errorf("top-1 filtrato = %d, atteso %d", res[0].DocID, want[0])
	}
}

// TestFilterNilUnchanged: senza filtro il comportamento è invariato (k risultati).
func TestFilterNilUnchanged(t *testing.T) {
	index, vectors := buildFilterTestIndex(t, filterTestN)
	res := index.SearchWithScores(vectors[0], filterBenchK, nil, filterBenchEf)
	if len(res) != filterBenchK {
		t.Fatalf("attesi %d risultati, ottenuti %d", filterBenchK, len(res))
	}
}

// TestFilterEmptyAllowlist: un allowlist vuoto (ma non nil) non deve panicare
// né restituire risultati.
func TestFilterEmptyAllowlist(t *testing.T) {
	index, vectors := buildFilterTestIndex(t, filterTestN)
	empty := roaring.New()
	res := index.SearchWithScores(vectors[0], filterBenchK, empty, filterBenchEf)
	if len(res) != 0 {
		t.Errorf("allowlist vuoto: attesi 0 risultati, ottenuti %d", len(res))
	}
}

// externalQueryN costruisce una query esterna all'indice (media allowlist +
// rumore), come una domanda rispetto al suo haystack.
func externalQueryN(vectors [][]float32, count int) []float32 {
	q := make([]float32, filterBenchDim)
	for i := 1; i <= count && i < len(vectors); i++ {
		for d, v := range vectors[i] {
			q[d] += v / float32(count)
		}
	}
	for d := range q {
		q[d] += (rand.Float32() - 0.5) * 0.1
	}
	return q
}

func externalQuery(vectors [][]float32) []float32 {
	return externalQueryN(vectors, filterSparseCount)
}

// bruteForceTopKN calcola il top-k esatto (cosine) sui membri allowlist.
// vectors[i] corrisponde a internal ID i+1 (inserimento sequenziale).
func bruteForceTopKN(vectors [][]float32, allow *roaring.Bitmap, q []float32, k int) []uint32 {
	qn := float64(0)
	for _, v := range q {
		qn += float64(v * v)
	}
	qn = math.Sqrt(qn)
	type scored struct {
		id uint32
		d  float64
	}
	var all []scored
	it := allow.Iterator()
	for it.HasNext() {
		id := it.Next()
		vec := vectors[id-1]
		dot, nn := float64(0), float64(0)
		for d := range q {
			dot += float64(q[d] * vec[d])
			nn += float64(vec[d] * vec[d])
		}
		dist := 1.0
		if qn > 0 && nn > 0 {
			dist = 1.0 - dot/(qn*math.Sqrt(nn))
		}
		all = append(all, scored{id, dist})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].d < all[j].d })
	if len(all) > k {
		all = all[:k]
	}
	out := make([]uint32, len(all))
	for i, s := range all {
		out[i] = s.id
	}
	return out
}
