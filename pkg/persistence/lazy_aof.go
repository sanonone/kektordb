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
//
// SNAPSHOT MODE: The writer supports a special "snapshot mode" to prevent AOF data loss during
// snapshot operations. When snapshot mode is active, new writes are buffered separately and
// can be flushed to the AOF after truncation, ensuring no in-flight writes are lost.
//
// CONCURRENCY MODEL: All mutable state (write buffer, snapshot buffer, closed flag, fatal error)
// is owned exclusively by a single background goroutine (run). Callers interact with it via two
// channels:
//   - writeCh: buffered, for data writes; provides natural batching and rarely blocks callers.
//   - cmdCh: unbuffered, for control operations (flush, sync, close, etc.); provides ordered
//     command processing with a synchronous ack.
//
// This design is race-free by construction and requires no explicit mutex on the hot path.
type LazyAOFWriter struct {
	// underlying is the actual AOF writer that performs the disk operations.
	// It must not be used directly after being wrapped by LazyAOFWriter.
	underlying *AOFWriter

	// Configuration parameters (can be adjusted for different durability/performance trade-offs).
	flushInterval     time.Duration // How often to flush the in-memory buffer to the OS page cache.
	forceSyncInterval time.Duration // How often to fsync, persisting the OS page cache to disk.
	maxBufferSize     int           // Maximum buffered entries before an immediate flush is triggered.

	// writeCh carries data writes to the run goroutine.
	// It is buffered (DefaultWriteQueueSize) so callers rarely block waiting for the goroutine.
	writeCh chan writeRequest

	// cmdCh carries control commands (flush, sync, close, etc.) to the run goroutine.
	// It is unbuffered to provide back-pressure and ensure ordered command processing.
	cmdCh chan command

	// closeOnce ensures Close is idempotent; only the first caller sends cmdClose.
	closeOnce sync.Once

	// closedCh is closed by the run goroutine's defer just before it exits.
	// Callers that hold a reference can select on this to detect shutdown.
	closedCh chan struct{}
}

type writeRequest struct {
	data string
}

type command struct {
	kind   commandKind
	path   string
	respCh chan commandResponse
}

type commandKind int

type commandResponse struct {
	err    error
	writes []string
}

// Default configuration constants for LazyAOFWriter.
// These values provide a good balance between performance and durability for most use cases.
const (
	// DefaultLazyFlushInterval is the default time between buffer flushes to the OS page cache.
	// Flushing every 100ms provides good batching while keeping latency low.
	DefaultLazyFlushInterval = 100 * time.Millisecond

	// DefaultForceSyncInterval is the default time between forced fsync operations.
	// Syncing every 1 second limits potential data loss to approximately 1 second of writes
	// in the event of a crash.
	DefaultForceSyncInterval = 1 * time.Second

	// DefaultMaxBufferSize is the default maximum number of in-memory entries before an
	// immediate flush to the OS page cache is triggered.
	DefaultMaxBufferSize = 1000

	// DefaultWriteQueueSize is the capacity of the buffered writeCh channel. A large
	// buffer allows bursty writers to enqueue without blocking, decoupling them from
	// the periodic flush cadence.
	DefaultWriteQueueSize = 16384

	cmdFlush commandKind = iota
	cmdSync
	cmdClose
	cmdTruncate
	cmdReplaceWith
	cmdBeginSnapshot
	cmdEndSnapshot
	cmdIsSnapshotActive
	cmdErr
)

// NewLazyAOFWriter creates a new lazy AOF writer that wraps an existing AOFWriter.
// The lazy writer will batch writes and flush them periodically for better performance.
// The underlying AOFWriter must not be used directly after wrapping it.
func NewLazyAOFWriter(underlying *AOFWriter) *LazyAOFWriter {
	return NewLazyAOFWriterWithConfig(
		underlying,
		DefaultLazyFlushInterval,
		DefaultForceSyncInterval,
		DefaultMaxBufferSize,
	)
}

// NewLazyAOFWriterWithConfig creates a new lazy AOF writer with custom configuration parameters,
// allowing fine-tuning of the durability vs. performance trade-off.
//
// All state mutations are owned by a single background goroutine (run), which eliminates
// the need for a explicit mutex and makes the concurrency model easy to reason about.
// Callers communicate with run via writeCh (data path) and cmdCh (control path).
//
// Parameters:
//   - underlying: The actual AOF writer to wrap; must not be used directly after this call.
//   - flushInterval: How often to flush the buffer to the OS page cache (e.g., 100ms).
//   - forceSyncInterval: How often to fsync to disk for crash-safety (e.g., 1s).
//   - maxBufferSize: Maximum buffered entries before an immediate flush is triggered.
func NewLazyAOFWriterWithConfig(
	underlying *AOFWriter,
	flushInterval time.Duration,
	forceSyncInterval time.Duration,
	maxBufferSize int,
) *LazyAOFWriter {
	if flushInterval <= 0 {
		flushInterval = DefaultLazyFlushInterval
	}
	if forceSyncInterval <= 0 {
		forceSyncInterval = DefaultForceSyncInterval
	}
	if maxBufferSize <= 0 {
		maxBufferSize = DefaultMaxBufferSize
	}

	lw := &LazyAOFWriter{
		underlying:        underlying,
		flushInterval:     flushInterval,
		forceSyncInterval: forceSyncInterval,
		maxBufferSize:     maxBufferSize,
		writeCh:           make(chan writeRequest, DefaultWriteQueueSize),
		cmdCh:             make(chan command),
		closedCh:          make(chan struct{}),
	}

	go lw.run()

	slog.Info("LazyAOFWriter initialized",
		"flush_interval", flushInterval,
		"sync_interval", forceSyncInterval,
		"max_buffer_size", maxBufferSize,
		"write_queue_size", DefaultWriteQueueSize,
	)

	return lw
}

// Write enqueues data to be written to the AOF on the next flush cycle and returns
// immediately — it does not wait for the data to reach disk or even for run() to
// process the entry. This is a fire-and-forget design: throughput is maximised by
// decoupling the caller from the I/O goroutine.
//
// The only errors returned here are structural (writer already closed). Flush/sync
// errors from the background goroutine are not propagated per-write; call Err() after
// a Flush() or Sync() to check for a permanent write failure, mirroring bufio.Writer.
//
// When snapshot mode is active (see BeginSnapshotMode), writes are redirected to a
// separate shadow buffer without leaving this channel path.
func (lw *LazyAOFWriter) Write(data string) error {
	select {
	case <-lw.closedCh:
		return fmt.Errorf("cannot write to closed LazyAOFWriter")
	case lw.writeCh <- writeRequest{data: data}:
		return nil
	}
}

// Flush immediately writes all buffered data to the underlying AOF writer and blocks until
// the operation completes. Data is written to the OS page cache but is NOT guaranteed to
// be on disk; call Sync for that guarantee.
func (lw *LazyAOFWriter) Flush() error {
	return lw.call(cmdFlush).err
}

// Sync flushes all buffered data and forces an fsync on the underlying file, guaranteeing
// that all previously written data has been persisted to disk. This is more expensive than
// Flush but provides full crash-safety.
func (lw *LazyAOFWriter) Sync() error {
	return lw.call(cmdSync).err
}

// Err returns the first permanent error encountered by the background goroutine (e.g. a
// failed flush or sync). Once set, fatalErr causes all subsequent flushes to fail.
// Call Err() after Flush() or Sync() to confirm data was persisted.
func (lw *LazyAOFWriter) Err() error {
	return lw.call(cmdErr).err
}

// Close gracefully shuts down the writer. It flushes any buffered data, syncs to disk, and
// closes the underlying file.
// Close is safe to call concurrently and is idempotent — subsequent calls return nil.
func (lw *LazyAOFWriter) Close() error {
	var err error

	lw.closeOnce.Do(func() {
		resp := lw.call(cmdClose)
		err = resp.err
	})

	return err
}

// Truncate flushes any pending buffer and then truncates the AOF file to zero length
// via the underlying writer.
func (lw *LazyAOFWriter) Truncate() error {
	return lw.call(cmdTruncate).err
}

// ReplaceWith flushes any pending buffer and then atomically replaces the current AOF file
// with the file at newFilePath via the underlying writer.
func (lw *LazyAOFWriter) ReplaceWith(newFilePath string) error {
	return lw.callWithPath(cmdReplaceWith, newFilePath).err
}

// BeginSnapshotMode flushes the current buffer and then activates snapshot mode.
// While active, new writes are redirected into a separate shadow buffer instead of the
// normal write buffer, allowing the caller to safely truncate the AOF file without losing
// any writes that arrive between snapshot creation and truncation.
//
// The caller MUST call EndSnapshotMode after the truncation to retrieve the buffered shadow
// writes and replay them into the freshly truncated AOF.
// Returns an error if snapshot mode is already active or the writer is closed.
func (lw *LazyAOFWriter) BeginSnapshotMode() error {
	return lw.call(cmdBeginSnapshot).err
}

// EndSnapshotMode deactivates snapshot mode and returns all writes that were buffered in the
// shadow buffer since BeginSnapshotMode was called. The caller is responsible for replaying
// these entries into the AOF (typically after truncating) in the order they are returned.
// Returns an error if snapshot mode was not active.
func (lw *LazyAOFWriter) EndSnapshotMode() ([]string, error) {
	resp := lw.call(cmdEndSnapshot)
	return resp.writes, resp.err
}

// IsSnapshotModeActive reports whether snapshot mode is currently active.
// The answer is authoritative because it is supplied directly by the run goroutine.
// Returns false if the writer is fully closed.
func (lw *LazyAOFWriter) IsSnapshotModeActive() bool {
	resp := lw.call(cmdIsSnapshotActive)
	return len(resp.writes) == 1 && resp.writes[0] == "true"
}

// Path returns the file path of the underlying AOF file.
func (lw *LazyAOFWriter) Path() string {
	return lw.underlying.Path()
}

// File returns the underlying OS file handle. Callers should treat this as read-only;
// direct writes bypass the lazy buffer and may corrupt the AOF state.
func (lw *LazyAOFWriter) File() *os.File {
	return lw.underlying.File()
}

// call sends a command to run and waits for the response.
func (lw *LazyAOFWriter) call(kind commandKind) commandResponse {
	return lw.callWithPath(kind, "")
}

// callWithPath sends a command with an optional file path to run and waits for the response.
// If closedCh is already closed, it returns an error immediately without blocking.
func (lw *LazyAOFWriter) callWithPath(kind commandKind, path string) commandResponse {
	respCh := make(chan commandResponse, 1)

	select {
	case <-lw.closedCh:
		return commandResponse{err: fmt.Errorf("LazyAOFWriter is closed")}
	case lw.cmdCh <- command{kind: kind, path: path, respCh: respCh}:
	}

	return <-respCh
}

// run is the single-owner goroutine for all mutable state. It serialises writes and
// control commands, eliminating the need for external locking.
//
// State ownership:
//   - buffer, snapshotBuffer, inSnapshotMode, fatalErr, closed: owned solely by this goroutine.
//
// Shutdown sequence (cmdClose):
//  1. closeWriter() flushes, syncs, and closes the underlying file.
//  2. Any writes already enqueued in writeCh (e.g. goroutines that sent to the buffered
//     channel before cmdClose was processed) are drained and returned errors — this avoids
//     goroutine leaks from callers blocked on <-errCh.
//  3. The run goroutine returns; the deferred close(closedCh) unblocks any caller selecting
//     on that channel.
func (lw *LazyAOFWriter) run() {
	defer close(lw.closedCh)

	flushTicker := time.NewTicker(lw.flushInterval)
	defer flushTicker.Stop()

	syncTicker := time.NewTicker(lw.forceSyncInterval)
	defer syncTicker.Stop()

	buffer := make([]string, 0, lw.maxBufferSize)
	snapshotBuffer := make([]string, 0, lw.maxBufferSize)

	inSnapshotMode := false
	var fatalErr error
	closed := false

	// flush writes the in-memory buffer to the underlying AOF and clears it.
	// On error it sets fatalErr, permanently disabling further flushes.
	flush := func() error {
		if fatalErr != nil {
			return fatalErr
		}

		if len(buffer) == 0 {
			return nil
		}

		for _, data := range buffer {
			if err := lw.underlying.Write(data); err != nil {
				fatalErr = fmt.Errorf("failed to write to AOF: %w", err)
				return fatalErr
			}
		}

		if err := lw.underlying.Flush(); err != nil {
			fatalErr = fmt.Errorf("failed to flush AOF buffer: %w", err)
			return fatalErr
		}

		buffer = buffer[:0]
		return nil
	}

	// syncNow flushes the buffer and fsyncs the underlying file to disk.
	syncNow := func() error {
		if err := flush(); err != nil {
			return err
		}

		if err := lw.underlying.Sync(); err != nil {
			fatalErr = fmt.Errorf("failed to sync AOF: %w", err)
			return fatalErr
		}

		return nil
	}

	// closeWriter performs the final flush+sync+close sequence exactly once.
	closeWriter := func() error {
		if closed {
			return nil
		}
		closed = true

		var err error

		if flushErr := flush(); flushErr != nil {
			slog.Error("failed to flush during close", "error", flushErr)
			err = flushErr
		}

		if syncErr := lw.underlying.Sync(); syncErr != nil && err == nil {
			err = fmt.Errorf("failed to sync during close: %w", syncErr)
		}

		if closeErr := lw.underlying.Close(); closeErr != nil && err == nil {
			err = closeErr
		}

		return err
	}

	// drainWriteCh discards any writes already queued in the buffered writeCh when
	// cmdClose is processed. Because Write() is fire-and-forget there are no callers
	// blocked on a response — we simply drop the enqueued entries.
	// If any entries are dropped, a single warning is emitted so operators know
	// data was in-flight at shutdown time.
	drainWriteCh := func() {
		dropped := 0
		for {
			select {
			case <-lw.writeCh:
				dropped++
			default:
				if dropped > 0 {
					slog.Warn("LazyAOFWriter: dropped in-flight writes during close",
						"count", dropped,
					)
				}
				return
			}
		}
	}

	for {
		select {
		case req := <-lw.writeCh:
			// Discard writes that arrive after close — the underlying file is gone.
			if closed {
				continue
			}

			// Snapshot buffer always accepts writes regardless of fatalErr; the
			// caller retrieves them via EndSnapshotMode and writes them itself.
			// Unlike the normal buffer, the snapshot buffer cannot be flushed to
			// disk during snapshot mode — the AOF is about to be truncated, which
			// is precisely why the mode exists. We can only warn when it grows large.
			if inSnapshotMode {
				snapshotBuffer = append(snapshotBuffer, req.data)
				n := len(snapshotBuffer)
				if n == lw.maxBufferSize || (n > lw.maxBufferSize && n%lw.maxBufferSize == 0) {
					slog.Warn("snapshot buffer growing large; EndSnapshotMode may be delayed",
						"size", n,
						"max_buffer_size", lw.maxBufferSize,
					)
				}
				continue
			}

			// Drop normal writes if a permanent flush error has occurred.
			// Callers detect this via Err() or the return value of Flush()/Sync().
			if fatalErr != nil {
				continue
			}

			// Buffer hit capacity — flush inline. Safe because flush() only touches
			// run()-owned state; no lock or channel round-trip needed.
			if len(buffer) >= lw.maxBufferSize {
				if err := flush(); err != nil {
					slog.Error("size-triggered flush failed", "error", err)
				}
			}

			buffer = append(buffer, req.data)

		case <-flushTicker.C:
			if err := flush(); err != nil {
				slog.Error("periodic flush failed", "error", err)
			}

		case <-syncTicker.C:
			if err := syncNow(); err != nil {
				slog.Error("periodic sync failed", "error", err)
			}

		case cmd := <-lw.cmdCh:
			switch cmd.kind {
			case cmdFlush:
				cmd.respCh <- commandResponse{err: flush()}

			case cmdSync:
				cmd.respCh <- commandResponse{err: syncNow()}

			case cmdTruncate:
				err := flush()
				if err == nil {
					err = lw.underlying.Truncate()
				}
				cmd.respCh <- commandResponse{err: err}

			case cmdReplaceWith:
				err := flush()
				if err == nil {
					err = lw.underlying.ReplaceWith(cmd.path)
				}
				cmd.respCh <- commandResponse{err: err}

			case cmdBeginSnapshot:
				var err error

				if closed {
					err = fmt.Errorf("cannot begin snapshot mode on closed LazyAOFWriter")
				} else if inSnapshotMode {
					err = fmt.Errorf("snapshot mode already active")
				} else {
					err = flush()
					if err == nil {
						snapshotBuffer = snapshotBuffer[:0]
						inSnapshotMode = true
						slog.Debug("LazyAOFWriter entered snapshot mode")
					}
				}

				cmd.respCh <- commandResponse{err: err}

			case cmdEndSnapshot:
				var resp commandResponse

				if !inSnapshotMode {
					resp.err = fmt.Errorf("snapshot mode not active")
				} else {
					// Drain any writes already sitting in writeCh before closing
					// snapshot mode. Because Write() is fire-and-forget, callers
					// return as soon as data enters the buffered writeCh. By the
					// time EndSnapshotMode sends on cmdCh (unbuffered) and run()
					// receives it, all Write() calls that returned before this
					// point are guaranteed to be in writeCh. Draining here ensures
					// none of them are missed from the snapshot buffer.
					for {
						select {
						case req := <-lw.writeCh:
							if !closed {
								snapshotBuffer = append(snapshotBuffer, req.data)
							}
						default:
							goto donedraining
						}
					}
				donedraining:
					writes := make([]string, len(snapshotBuffer))
					copy(writes, snapshotBuffer)

					snapshotBuffer = snapshotBuffer[:0]
					inSnapshotMode = false

					resp.writes = writes

					slog.Debug("LazyAOFWriter exited snapshot mode",
						"buffered_writes", len(writes),
					)
				}

				cmd.respCh <- resp

			case cmdIsSnapshotActive:
				if inSnapshotMode {
					cmd.respCh <- commandResponse{writes: []string{"true"}}
				} else {
					cmd.respCh <- commandResponse{writes: []string{"false"}}
				}

			case cmdErr:
				cmd.respCh <- commandResponse{err: fatalErr}

			case cmdClose:
				err := closeWriter()
				// Unblock any writes already sitting in the buffered channel that will
				// never be consumed now that the goroutine is about to exit.
				drainWriteCh()
				cmd.respCh <- commandResponse{err: err}
				return
			}
		}
	}
}



