package persistence

import (
	"bufio"
	"fmt"
	"os"
	"sync"
)

// AOFWriter manages writing to the Append-Only File using a robust binary protocol.
type AOFWriter struct {
	mu   sync.Mutex
	file *os.File
	buf  *bufio.Writer
	path string

	// frameWriter handles the TLVC (Type-Length-Value-Checksum) encoding.
	// It writes directly into 'buf'.
	frameWriter *FrameWriter
}

// NewAOFWriter opens or creates an AOF file at the given path.
func NewAOFWriter(path string) (*AOFWriter, error) {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		return nil, fmt.Errorf("failed to open AOF file: %w", err)
	}

	// Use a buffered writer to minimize syscalls.
	// 4KB default buffer is usually fine, but can be increased for high throughput.
	buf := bufio.NewWriter(file)

	return &AOFWriter{
		file: file,
		buf:  buf,
		path: path,
		// Initialize FrameWriter wrapping the buffer.
		// All subsequent writes will go through this FrameWriter to ensure consistency.
		frameWriter: NewFrameWriter(buf),
	}, nil
}

// Write appends a raw RESP command string to the AOF file.
// It wraps the command in a binary frame with a checksum to ensure integrity.
//
// Unlike the previous version which wrote raw strings, this method now guarantees
// that partial writes can be detected during recovery.
func (a *AOFWriter) Write(data string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Convert the string command (RESP) to bytes and wrap it in a frame.
	// Frame Format: [Magic(1)][Op(1)][Len(4)][CRC(4)][Data(N)]
	if err := a.frameWriter.WriteFrame([]byte(data)); err != nil {
		return err
	}

	return nil
}

// Flush forces the buffer contents to be written to the OS file descriptor.
func (a *AOFWriter) Flush() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.buf.Flush()
}

// Sync forces a flush to disk (fsync).
func (a *AOFWriter) Sync() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := a.buf.Flush(); err != nil {
		return err
	}
	return a.file.Sync()
}

// Close closes the underlying file.
func (a *AOFWriter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	var flushErr error
	if err := a.buf.Flush(); err != nil {
		flushErr = fmt.Errorf("flush failed: %w", err)
	}

	if err := a.file.Close(); err != nil {
		if flushErr != nil {
			return fmt.Errorf("close failed after flush error: %w (previous: %v)", err, flushErr)
		}
		return fmt.Errorf("close failed: %w", err)
	}

	return flushErr
}

// Truncate clears the file content. Used during rewriting/snapshotting.
func (a *AOFWriter) Truncate() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Reset buffer state to discard any pending data
	a.buf.Reset(a.file)

	if err := a.file.Truncate(0); err != nil {
		return err
	}
	_, err := a.file.Seek(0, 0)
	return err
}

// Path returns the file path.
func (a *AOFWriter) Path() string {
	return a.path
}

// File returns the underlying OS file (read-only access recommended or for specialized ops like Stat).
func (a *AOFWriter) File() *os.File {
	return a.file
}

// ReplaceWith replaces the current AOF file with a new one atomically (rename) and reopens it.
// Used at the end of AOF rewriting.
func (a *AOFWriter) ReplaceWith(newFilePath string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 1. Flush & Close old
	if err := a.buf.Flush(); err != nil {
		return fmt.Errorf("failed to flush buffer before replace: %w", err)
	}
	if err := a.file.Close(); err != nil {
		// Log error but continue, as data is likely flushed
		fmt.Fprintf(os.Stderr, "Warning: failed to close AOF file before replace: %v\n", err)
	}

	// 2. Rename (Atomic operation on POSIX systems)
	if err := os.Rename(newFilePath, a.path); err != nil {
		return fmt.Errorf("failed to replace AOF file: %w", err)
	}

	// 3. Reopen the new file
	file, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		return fmt.Errorf("failed to reopen AOF file after replace: %w", err)
	}

	// 4. Update internal references
	a.file = file
	// Reset the buffer to point to the new file descriptor
	a.buf.Reset(file)
	// FrameWriter uses the buf pointer, which hasn't changed, but the buf internals have.
	// Since FrameWriter holds an io.Writer interface pointing to a.buf, it remains valid.

	return nil
}
