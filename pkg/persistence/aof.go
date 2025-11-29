package persistence

import (
	"bufio"
	"fmt"
	"os"
	"sync"
)

// AOFWriter manages writing to the Append-Only File.
type AOFWriter struct {
	mu   sync.Mutex
	file *os.File
	buf  *bufio.Writer
	path string
}

// NewAOFWriter opens or creates an AOF file at the given path.
func NewAOFWriter(path string) (*AOFWriter, error) {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		return nil, fmt.Errorf("failed to open AOF file: %w", err)
	}

	return &AOFWriter{
		file: file,
		buf:  bufio.NewWriter(file), // 4kb buf (default)
		path: path,
	}, nil
}

// Write appends a raw string (RESP command) to the AOF file securely.
func (a *AOFWriter) Write(data string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, err := a.buf.WriteString(data); err != nil {
		return err
	}
	return nil
}

// Flush forces the buffer contents to be written to the os file descriptor.
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

	if err := a.buf.Flush(); err != nil {
		_ = a.file.Close()
		return err
	}
	return a.file.Close()
}

// Truncate clears the file content. Used during rewriting/snapshotting.
func (a *AOFWriter) Truncate() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Reset buffer
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
	_ = a.buf.Flush()
	_ = a.file.Close()

	// 2. Rename
	if err := os.Rename(newFilePath, a.path); err != nil {
		return fmt.Errorf("failed to replace AOF file: %w", err)
	}

	// 3. Reopen
	file, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		return fmt.Errorf("failed to reopen AOF file after replace: %w", err)
	}
	a.file = file
	a.buf.Reset(file)
	return nil
}
