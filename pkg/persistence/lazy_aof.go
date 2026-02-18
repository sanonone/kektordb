package persistence

import (
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// LazyAOFWriter provides asynchronous, batched write operations for the AOF (Append-Only File).
// This implementation improves write throughput by buffering operations and flushing them
// periodically or when the buffer reaches a certain size, rather than flushing on every write.
//
// IMPORTANT: Durability guarantees:
// - Data is flushed periodically based on time (default: every 100ms) or buffer size (default: 1000 entries)
// - A forced sync to disk occurs every 1 second to ensure data persistence
// - On Close(), all pending data is flushed and synced to disk
// - In case of crash, maximum data loss window is ~1 second (configurable via ForceSyncInterval)
//
// This trade-off is suitable for high-throughput scenarios where slight durability delay
// is acceptable in exchange for significantly improved write performance (10-100x throughput improvement).
type LazyAOFWriter struct {
	// underlying is the actual AOF writer that performs the disk operations
	underlying *AOFWriter

	// buffer holds pending write operations before flushing
	buffer []string

	// mu protects the buffer and other internal state
	mu sync.Mutex

	// flushTicker triggers periodic flushes based on time
	flushTicker *time.Ticker

	// syncTicker triggers periodic forced fsync operations for durability
	syncTicker *time.Ticker

	// stopCh signals the background goroutines to stop
	stopCh chan struct{}

	// stopped indicates whether the writer has been closed
	stopped bool

	// Configuration parameters (can be adjusted for different durability/performance trade-offs)
	flushInterval     time.Duration // How often to flush buffer to OS
	forceSyncInterval time.Duration // How often to force fsync to disk
	maxBufferSize     int           // Maximum buffer size before forced flush
}

// Default configuration constants for LazyAOFWriter.
// These values provide a good balance between performance and durability for most use cases.
const (
	// DefaultLazyFlushInterval is the default time between buffer flushes to the OS.
	// Flushing every 100ms provides good batching while keeping latency low.
	DefaultLazyFlushInterval = 100 * time.Millisecond

	// DefaultForceSyncInterval is the default time between forced fsync operations.
	// Syncing every 1 second ensures that data is persisted to disk regularly,
	// limiting potential data loss to approximately 1 second of writes in case of crash.
	DefaultForceSyncInterval = 1 * time.Second

	// DefaultMaxBufferSize is the default maximum number of entries in the buffer.
	// When the buffer reaches this size, a flush is triggered immediately.
	DefaultMaxBufferSize = 1000
)

// NewLazyAOFWriter creates a new lazy AOF writer that wraps an existing AOFWriter.
// The lazy writer will batch writes and flush them periodically for better performance.
// The underlying AOFWriter should not be used directly after wrapping it.
func NewLazyAOFWriter(underlying *AOFWriter) *LazyAOFWriter {
	return NewLazyAOFWriterWithConfig(
		underlying,
		DefaultLazyFlushInterval,
		DefaultForceSyncInterval,
		DefaultMaxBufferSize,
	)
}

// NewLazyAOFWriterWithConfig creates a new lazy AOF writer with custom configuration parameters.
// This allows fine-tuning of the durability vs performance trade-off.
//
// Parameters:
//   - underlying: The actual AOF writer to wrap
//   - flushInterval: How often to flush the buffer to the OS (e.g., 100ms)
//   - forceSyncInterval: How often to force fsync to disk for durability (e.g., 1s)
//   - maxBufferSize: Maximum buffer entries before forced flush (e.g., 1000)
func NewLazyAOFWriterWithConfig(
	underlying *AOFWriter,
	flushInterval time.Duration,
	forceSyncInterval time.Duration,
	maxBufferSize int,
) *LazyAOFWriter {
	lw := &LazyAOFWriter{
		underlying:        underlying,
		buffer:            make([]string, 0, maxBufferSize),
		flushInterval:     flushInterval,
		forceSyncInterval: forceSyncInterval,
		maxBufferSize:     maxBufferSize,
		stopCh:            make(chan struct{}),
	}

	// Start background flush routine
	lw.flushTicker = time.NewTicker(flushInterval)
	go lw.flushRoutine()

	// Start background sync routine for durability
	lw.syncTicker = time.NewTicker(forceSyncInterval)
	go lw.syncRoutine()

	slog.Info("LazyAOFWriter initialized",
		"flush_interval", flushInterval,
		"sync_interval", forceSyncInterval,
		"max_buffer_size", maxBufferSize,
	)

	return lw
}

// Write appends data to the internal buffer for later flushing.
// This method is non-blocking and returns immediately after adding data to the buffer.
// The actual disk write happens asynchronously in the background.
// If the buffer reaches maxBufferSize, an immediate flush is triggered.
func (lw *LazyAOFWriter) Write(data string) error {
	lw.mu.Lock()
	defer lw.mu.Unlock()

	if lw.stopped {
		return fmt.Errorf("cannot write to closed LazyAOFWriter")
	}

	// Add data to buffer
	lw.buffer = append(lw.buffer, data)

	// If buffer is full, trigger immediate flush in background
	if len(lw.buffer) >= lw.maxBufferSize {
		go lw.Flush()
	}

	return nil
}

// Flush immediately writes all buffered data to the underlying AOF writer.
// This method blocks until the flush is complete.
// Note: This only flushes to the OS buffer, not necessarily to disk (use Sync for that).
func (lw *LazyAOFWriter) Flush() error {
	lw.mu.Lock()
	defer lw.mu.Unlock()

	return lw.flushUnlocked()
}

// flushUnlocked performs the actual flush operation.
// Caller must hold the mutex.
func (lw *LazyAOFWriter) flushUnlocked() error {
	if len(lw.buffer) == 0 {
		return nil
	}

	// Write all buffered entries to the underlying AOF
	for _, data := range lw.buffer {
		if err := lw.underlying.Write(data); err != nil {
			return fmt.Errorf("failed to write to AOF: %w", err)
		}
	}

	// Flush the underlying buffer to OS
	if err := lw.underlying.Flush(); err != nil {
		return fmt.Errorf("failed to flush AOF buffer: %w", err)
	}

	// Clear the buffer
	lw.buffer = lw.buffer[:0]

	return nil
}

// Sync forces a flush to disk (fsync) for durability.
// This first flushes any pending buffer, then calls fsync on the underlying file.
func (lw *LazyAOFWriter) Sync() error {
	lw.mu.Lock()
	defer lw.mu.Unlock()

	// First flush any pending writes
	if err := lw.flushUnlocked(); err != nil {
		return err
	}

	// Then force sync to disk
	return lw.underlying.Sync()
}

// Close gracefully shuts down the lazy writer.
// This stops the background routines, flushes any pending data, and syncs to disk.
// After Close(), no more writes are accepted.
func (lw *LazyAOFWriter) Close() error {
	lw.mu.Lock()
	if lw.stopped {
		lw.mu.Unlock()
		return fmt.Errorf("LazyAOFWriter already closed")
	}
	lw.stopped = true
	lw.mu.Unlock()

	// Stop background routines
	close(lw.stopCh)
	lw.flushTicker.Stop()
	lw.syncTicker.Stop()

	// Final flush and sync to ensure all data is persisted
	lw.mu.Lock()
	defer lw.mu.Unlock()

	if err := lw.flushUnlocked(); err != nil {
		slog.Error("Failed to flush during Close", "error", err)
		// Continue to try closing underlying even if flush failed
	}

	return lw.underlying.Close()
}

// Path returns the file path of the underlying AOF writer.
func (lw *LazyAOFWriter) Path() string {
	return lw.underlying.Path()
}

// File returns the underlying OS file (read-only access recommended).
func (lw *LazyAOFWriter) File() *os.File {
	return lw.underlying.File()
}

// Truncate clears the file content via the underlying writer.
// This is a synchronous operation that also flushes any pending buffer first.
func (lw *LazyAOFWriter) Truncate() error {
	lw.mu.Lock()
	defer lw.mu.Unlock()

	// First flush any pending writes
	if err := lw.flushUnlocked(); err != nil {
		return err
	}

	return lw.underlying.Truncate()
}

// ReplaceWith replaces the current AOF file with a new one atomically.
// This first flushes any pending buffer to ensure data consistency.
func (lw *LazyAOFWriter) ReplaceWith(newFilePath string) error {
	lw.mu.Lock()
	defer lw.mu.Unlock()

	// First flush any pending writes to ensure consistency
	if err := lw.flushUnlocked(); err != nil {
		return err
	}

	return lw.underlying.ReplaceWith(newFilePath)
}

// flushRoutine runs in a background goroutine and periodically flushes the buffer.
func (lw *LazyAOFWriter) flushRoutine() {
	for {
		select {
		case <-lw.flushTicker.C:
			if err := lw.Flush(); err != nil {
				slog.Error("Periodic flush failed", "error", err)
			}
		case <-lw.stopCh:
			return
		}
	}
}

// syncRoutine runs in a background goroutine and periodically forces fsync.
// This ensures data durability by regularly syncing to disk, even if the buffer
// hasn't filled up or the flush interval hasn't triggered yet.
func (lw *LazyAOFWriter) syncRoutine() {
	for {
		select {
		case <-lw.syncTicker.C:
			if err := lw.Sync(); err != nil {
				slog.Error("Periodic sync failed", "error", err)
			}
		case <-lw.stopCh:
			return
		}
	}
}
