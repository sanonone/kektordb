package persistence

import (
	"fmt"
	"os"
	"sync"
)

// AOFWriter manages thread-safe writing to the Append-Only File.
type AOFWriter struct {
	mu   sync.Mutex
	file *os.File
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
		path: path,
	}, nil
}

// Write appends a raw string (RESP command) to the AOF file securely.
func (a *AOFWriter) Write(data string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, err := a.file.WriteString(data); err != nil {
		return err
	}
	return nil
}

// Sync forces a flush to disk (fsync).
func (a *AOFWriter) Sync() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.file.Sync()
}

// Close closes the underlying file.
func (a *AOFWriter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.file.Close()
}

// Truncate clears the file content. Used during rewriting/snapshotting.
func (a *AOFWriter) Truncate() error {
	a.mu.Lock()
	defer a.mu.Unlock()

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
	// Nota: non è thread safe usarlo direttamente per scrivere senza lock
	return a.file
}

// ReplaceWith replaces the current AOF file with a new one atomically (rename) and reopens it.
// Used at the end of AOF rewriting.
func (a *AOFWriter) ReplaceWith(newFilePath string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 1. Chiudi il vecchio
	_ = a.file.Close()

	// 2. Rinomina il nuovo sopra il vecchio
	if err := os.Rename(newFilePath, a.path); err != nil {
		return fmt.Errorf("failed to replace AOF file: %w", err)
	}

	// 3. Riapri il file (che ora ha il contenuto nuovo)
	file, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		return fmt.Errorf("failed to reopen AOF file after replace: %w", err)
	}
	a.file = file
	return nil
}
