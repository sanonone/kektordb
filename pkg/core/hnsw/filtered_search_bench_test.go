package hnsw

// Regression test per il fix D2 (filtered search sparso) + bench performance.
// - I Test verificano esattezza (tier brute-force) e membership (tier traverse-through).
// - I Benchmark misurano latenza non filtrata (hot path, non deve regredire)
//   e filtrata. Eseguire con:
//   go test -bench 'BenchmarkFilterBaseline' -benchtime=200x -run XXX ./pkg/core/hnsw/

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"

	"github.com/RoaringBitmap/roaring"
	"github.com/sanonone/kektordb/pkg/core/distance"
)

const (
	filterBenchN      = 20000
	filterBenchDim    = 128
	filterBenchK      = 20
	filterBenchEf     = 100
	filterSparseCount = 40 // 0.2% — come question_id su S-full
	// Dimensioni ridotte per gli unit test veloci (CI): bastano a esercitare
	// entrambi i tier (soglia brute-force = 5000).
	filterTestN       = 2000
	filterTestSparse  = 10
	filterTestNMed    = 8000
	filterTestMedFrac = 0.75 // 6000 membri > soglia -> tier traverse-through
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
	for i, v := range vectors {
		id := embeddingID(i)
		if _, err := index.Add(id, v); err != nil {
			b.Fatalf("Add %d: %v", i, err)
		}
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

// buildFilterTestIndex costruisce un indice piccolo per gli unit test veloci.
func buildFilterTestIndex(t *testing.T, n int) (*Index, [][]float32) {
	t.Helper()
	vectors := buildVectorPool(n, filterBenchDim)
	index, err := New(BenchM, BenchEf, distance.Cosine, distance.Float32, "", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i, v := range vectors {
		if _, err := index.Add(embeddingID(i), v); err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
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

// TestFilteredSparseExact verifica che con allowlist piccola (sotto soglia
// brute-force) i risultati siano ESATTAMENTE il top-k brute-force.
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

// TestFilteredTraversalMembership verifica il tier traverse-through
// (allowlist sopra soglia): tutti i risultati devono appartenere al filtro.
func TestFilteredTraversalMembership(t *testing.T) {
	index, vectors := buildFilterTestIndex(t, filterTestNMed)
	nMed := int(float64(filterTestNMed) * filterTestMedFrac)
	dense := sparseAllowlist(nMed)
	q := externalQueryN(vectors, 40)
	res := index.SearchWithScores(q, filterBenchK, dense, filterBenchEf)
	if len(res) != filterBenchK {
		t.Fatalf("attesi %d risultati, ottenuti %d", filterBenchK, len(res))
	}
	for _, r := range res {
		if !dense.Contains(r.DocID) {
			t.Fatalf("DocID %d fuori allowlist", r.DocID)
		}
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

// externalQuery costruisce una query esterna all'indice (media allowlist +
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

// bruteForceTopK calcola il top-k esatto (cosine) sui membri allowlist.
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
