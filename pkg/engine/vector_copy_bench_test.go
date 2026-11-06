package engine

import (
	"fmt"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// Benchmark for the decision in docs/RECONNAISSANCE-2.4-vector-aliasing.md:
// copy vectors at the API boundary (safe, costs allocations) or keep the alias
// (fast, the read-after-close fault stays possible).
//
// The question is what VGet and VGetMany cost with and without the copy.
// Run: go test -bench BenchmarkVGet -benchmem ./pkg/engine/

func setupVectorBench(b *testing.B, count, dim int) *Engine {
	b.Helper()
	dir := b.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := Open(opts)
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	if err := eng.VCreate("bench", distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		b.Fatalf("vcreate: %v", err)
	}

	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = float32(i%17) / 17.0
	}
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("id_%d", i)
		if err := eng.VAdd("bench", id, vec, map[string]any{"content": "x"}); err != nil {
			b.Fatalf("vadd %s: %v", id, err)
		}
	}
	b.Cleanup(func() { eng.Close() })
	return eng
}

// BenchmarkVGetSingle measures one read of a 384-dim vector.
func BenchmarkVGetSingle(b *testing.B) {
	eng := setupVectorBench(b, 100, 384)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := eng.VGet("bench", "id_50"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkVGetSingleCopy measures the same read with the boundary copy the
// fix would add.
func BenchmarkVGetSingleCopy(b *testing.B) {
	eng := setupVectorBench(b, 100, 384)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		d, err := eng.VGet("bench", "id_50")
		if err != nil {
			b.Fatal(err)
		}
		_ = append([]float32(nil), d.Vector...)
	}
}

// BenchmarkVGetMany100 is the hydration case: 100 vectors in one call.
func BenchmarkVGetMany100(b *testing.B) {
	eng := setupVectorBench(b, 100, 384)
	ids := make([]string, 100)
	for i := range ids {
		ids[i] = fmt.Sprintf("id_%d", i)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := eng.VGetMany("bench", ids); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkVGetMany100Copy adds the copy for all 100.
func BenchmarkVGetMany100Copy(b *testing.B) {
	eng := setupVectorBench(b, 100, 384)
	ids := make([]string, 100)
	for i := range ids {
		ids[i] = fmt.Sprintf("id_%d", i)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		res, err := eng.VGetMany("bench", ids)
		if err != nil {
			b.Fatal(err)
		}
		for j := range res {
			res[j].Vector = append([]float32(nil), res[j].Vector...)
		}
	}
}

// BenchmarkVSearchUnaffected proves the search hot path is untouched by the
// boundary copy: the distance functions read the arena directly.
func BenchmarkVSearchUnaffected(b *testing.B) {
	eng := setupVectorBench(b, 1000, 384)
	query := make([]float32, 384)
	for i := range query {
		query[i] = 0.01
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := eng.VSearchWithScores("bench", query, 10, "", 0); err != nil {
			b.Fatal(err)
		}
	}
}
