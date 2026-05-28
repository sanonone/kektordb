package persistence

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// BenchmarkWrite_WritersAndFlusher — realistic production load:
// N writer goroutines running concurrently with 1 flusher goroutine.
// Write() is fire-and-forget (writeCh); Flush() goes through cmdCh.
// These two paths are independent so writers are never blocked by flushes.
// ---------------------------------------------------------------------------

// BenchmarkWrite_WritersAndFlusher measures Write() throughput when a concurrent
// flusher goroutine calls Flush() in a tight loop — the production pattern where
// a background ticker flushes while request handlers are writing.
func BenchmarkWrite_WritersAndFlusher(b *testing.B) {
	for _, writers := range []int{1, 4, 16} {
		writers := writers
		b.Run(fmt.Sprintf("writers=%d", writers), func(b *testing.B) {
			// Disable the internal flush ticker so only our explicit flusher runs.
			lw := newBenchWriter(b, 1*time.Hour, 1*time.Hour, 1000)
			defer lw.Close()

			data := "SET key value"

			// One dedicated goroutine calling Flush() in a tight loop.
			stop := make(chan struct{})
			var flusherWg sync.WaitGroup
			flusherWg.Add(1)
			go func() {
				defer flusherWg.Done()
				for {
					select {
					case <-stop:
						return
					default:
						_ = lw.Flush()
					}
				}
			}()

			b.ResetTimer()
			b.ReportAllocs()
			b.SetParallelism(writers)
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if err := lw.Write(data); err != nil {
						b.Error(err)
					}
				}
			})
			b.StopTimer()
			close(stop)
			flusherWg.Wait()
		})
	}
}

// newBenchWriter creates a LazyAOFWriter backed by a temp file for benchmarks.
func newBenchWriter(b *testing.B, flushInterval, syncInterval time.Duration, maxBuf int) *LazyAOFWriter {
	b.Helper()
	path := filepath.Join(b.TempDir(), "bench.aof")
	underlying, err := NewAOFWriter(path, 0)
	if err != nil {
		b.Fatalf("NewAOFWriter: %v", err)
	}
	return NewLazyAOFWriterWithConfig(underlying, flushInterval, syncInterval, maxBuf)
}

// BenchmarkWrite_Sequential measures single-goroutine write throughput.
// Captures the per-call cost of a fire-and-forget send into writeCh.
func BenchmarkWrite_Sequential(b *testing.B) {
	lw := newBenchWriter(b, 100*time.Millisecond, 10*time.Second, 1000)
	defer lw.Close()

	data := "SET key value"
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		if err := lw.Write(data); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWrite_Concurrent measures write throughput with multiple goroutines
// contending on writeCh. Shows whether the buffered channel is the bottleneck
// or whether run()'s flush loop is.
func BenchmarkWrite_Concurrent(b *testing.B) {
	for _, goroutines := range []int{2, 8, 32} {
		goroutines := goroutines
		b.Run(fmt.Sprintf("goroutines=%d", goroutines), func(b *testing.B) {
			lw := newBenchWriter(b, 100*time.Millisecond, 10*time.Second, 1000)
			defer lw.Close()

			data := "SET key value"
			b.ResetTimer()
			b.ReportAllocs()

			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if err := lw.Write(data); err != nil {
						b.Error(err)
					}
				}
			})
		})
	}
}

// BenchmarkWrite_SmallBuffer measures write throughput when the buffer fills
// frequently, triggering inline flushes in the run() select loop. Compare with
// BenchmarkWrite_Sequential to see the overhead of frequent flush calls.
func BenchmarkWrite_SmallBuffer(b *testing.B) {
	// Buffer of 10 means flush triggers every 10 writes.
	lw := newBenchWriter(b, 100*time.Millisecond, 10*time.Second, 10)
	defer lw.Close()

	data := "SET key value"
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		if err := lw.Write(data); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWrite_LargePayload measures how payload size affects throughput.
// Channel overhead is constant; this surfaces the disk-write contribution.
func BenchmarkWrite_LargePayload(b *testing.B) {
	// Simulate a realistic vector upsert command (~256 bytes).
	data := fmt.Sprintf("HSET vector:%d embedding %s", 1, make([]byte, 224))

	lw := newBenchWriter(b, 100*time.Millisecond, 10*time.Second, 1000)
	defer lw.Close()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		if err := lw.Write(data); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFlush measures the cost of an explicit synchronous Flush() call.
// Useful to understand latency callers pay when they need a flush guarantee.
func BenchmarkFlush(b *testing.B) {
	lw := newBenchWriter(b, 1*time.Hour, 1*time.Hour, 1000) // disable automatic flushing
	defer lw.Close()

	data := "SET key value"
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Fill a small batch then flush.
		for j := 0; j < 10; j++ {
			if err := lw.Write(data); err != nil {
				b.Fatal(err)
			}
		}
		if err := lw.Flush(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkClose measures the shutdown cost — useful when Close() is on the
// hot path during snapshot/compaction cycles.
func BenchmarkClose(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		lw := newBenchWriter(b, 100*time.Millisecond, 10*time.Second, 1000)
		// Pre-fill a batch to make close do real work.
		for j := 0; j < 100; j++ {
			_ = lw.Write("SET key value")
		}
		b.StartTimer()

		if err := lw.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWrite_Concurrent_Close verifies there are no goroutine leaks or
// panics when Close races with concurrent writers. Not a pure throughput
// benchmark — use -race to catch races here.
func BenchmarkWrite_Concurrent_Close(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		lw := newBenchWriter(b, 100*time.Millisecond, 10*time.Second, 1000)
		var wg sync.WaitGroup
		b.StartTimer()

		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 50; j++ {
					_ = lw.Write("SET key value") // expect errors after close — that's fine
				}
			}()
		}

		_ = lw.Close()
		wg.Wait()
	}
}
