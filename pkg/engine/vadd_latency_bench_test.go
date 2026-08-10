package engine

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/types"
	"github.com/sanonone/kektordb/pkg/persistence"
)

// percentile returns the p-th percentile (0-100) of a sorted duration slice.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// BenchmarkVAddLatency_Serial measures the latency of individual VAdd calls
// (p50/p95/p99) without any background write load.
func BenchmarkVAddLatency_Serial(b *testing.B) {
	benchVAddLatency(b, false)
}

// BenchmarkVAddLatency_UnderBatchLoad measures the same percentiles while a
// background goroutine hammers VAddBatch — the write queue fills and single
// VAdds compete for the AOF channel.
func BenchmarkVAddLatency_UnderBatchLoad(b *testing.B) {
	benchVAddLatency(b, true)
}

func benchVAddLatency(b *testing.B, withLoad bool) {
	testDir := b.TempDir()
	eng, err := Open(DefaultOptions(testDir))
	if err != nil {
		b.Fatal(err)
	}
	defer eng.Close()

	if err := eng.VCreate("bench", "cosine", 16, 200, "float32", "english", nil, nil, nil); err != nil {
		b.Fatal(err)
	}

	dim := 384
	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = float32(i) / float32(dim)
	}
	meta := map[string]any{"content": "benchmark memory", "type": "memory", "tags": "bench"}

	if withLoad {
		stop := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			items := make([]types.BatchObject, 200)
			for i := range items {
				items[i] = types.BatchObject{Id: fmt.Sprintf("load%d", i), Vector: vec, Metadata: meta}
			}
			for {
				select {
				case <-stop:
					return
				default:
					_ = eng.VAddBatch("bench", items)
				}
			}
		}()
		defer func() {
			close(stop)
			wg.Wait()
		}()
	}

	// Warm up.
	if err := eng.VAdd("bench", "warmup", vec, meta); err != nil {
		b.Fatal(err)
	}

	samples := make([]time.Duration, 0, 1000)
	for i := 0; i < 1000; i++ {
		id := fmt.Sprintf("mem%d", i)
		start := time.Now()
		if err := eng.VAdd("bench", id, vec, meta); err != nil {
			b.Fatal(err)
		}
		samples = append(samples, time.Since(start))
	}

	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	p50 := percentile(samples, 50)
	p95 := percentile(samples, 95)
	p99 := percentile(samples, 99)

	b.ReportMetric(float64(p50.Microseconds()), "p50_us")
	b.ReportMetric(float64(p95.Microseconds()), "p95_us")
	b.ReportMetric(float64(p99.Microseconds()), "p99_us")
	b.StopTimer()
}

// BenchmarkVAddSerialization isolates the serialization cost of the AOF write
// path (vector string + RESP formatting) per single VAdd.
func BenchmarkVAddSerialization(b *testing.B) {
	dim := 384
	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = float32(i) / float32(dim)
	}
	metaBytes := []byte(`{"content":"benchmark","type":"memory"}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vecStr := float32SliceToHexString(vec)
		_ = persistence.FormatCommand("VADD", []byte("bench"), []byte("id"), []byte(vecStr), metaBytes)
	}
}
