package mmap

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"unsafe"
)

const (
	// DefaultChunkSize is 64MB.
	DefaultChunkSize = 64 * 1024 * 1024
)

// Chunk represents a single memory-mapped file.
type Chunk struct {
	ID   int
	File *os.File
	Data []byte
}

// VectorArena manages memory-mapped chunks to store vectors by sequential internal IDs.
type VectorArena struct {
	mu         sync.RWMutex
	dir        string
	chunkSize  int
	vectorSize int // Size of a single vector in bytes
	vecsPerChk int // Number of vectors that fit in one chunk
	chunks     []*Chunk
}

// NewVectorArena initializes the arena.
// vectorSize is the size in bytes (e.g., dim * 4 for Float32).
func NewVectorArena(dir string, vectorSize int) (*VectorArena, error) {
	if vectorSize <= 0 {
		return nil, fmt.Errorf("vectorSize must be > 0")
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create arena dir: %w", err)
	}

	vecsPerChk := DefaultChunkSize / vectorSize
	if vecsPerChk == 0 {
		return nil, fmt.Errorf("vector size %d exceeds chunk size", vectorSize)
	}

	va := &VectorArena{
		dir:        dir,
		chunkSize:  DefaultChunkSize,
		vectorSize: vectorSize,
		vecsPerChk: vecsPerChk,
		chunks:     make([]*Chunk, 0),
	}

	// Load existing chunks if they exist (for restart recovery)
	if err := va.loadExistingChunks(); err != nil {
		return nil, err
	}

	return va, nil
}

func (va *VectorArena) loadExistingChunks() error {
	entries, err := os.ReadDir(va.dir)
	if err != nil {
		return err
	}

	maxChunkID := -1
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		var id int
		if _, err := fmt.Sscanf(entry.Name(), "arena_%04d.bin", &id); err == nil {
			if id > maxChunkID {
				maxChunkID = id
			}
		}
	}

	// Open chunks up to maxChunkID sequentially
	for i := 0; i <= maxChunkID; i++ {
		if err := va.addChunk(i); err != nil {
			return err
		}
	}
	return nil
}

func (va *VectorArena) addChunk(chunkID int) error {
	fileName := filepath.Join(va.dir, fmt.Sprintf("arena_%04d.bin", chunkID))

	file, err := os.OpenFile(fileName, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}

	// Ensure file is exactly chunkSize
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() < int64(va.chunkSize) {
		if err := file.Truncate(int64(va.chunkSize)); err != nil {
			file.Close()
			return err
		}
	}

	data, err := mmapFile(file.Fd(), va.chunkSize)
	if err != nil {
		file.Close()
		return err
	}

	chunk := &Chunk{
		ID:   chunkID,
		File: file,
		Data: data,
	}

	va.chunks = append(va.chunks, chunk)
	return nil
}

// GetBytes returns a slice of bytes pointing to the memory-mapped region for the given ID.
// If the ID requires a chunk that doesn't exist yet, it creates it.
func (va *VectorArena) GetBytes(internalID uint32) ([]byte, error) {
	chunkID := int(internalID) / va.vecsPerChk
	offset := (int(internalID) % va.vecsPerChk) * va.vectorSize

	// Check if we need to map new chunks
	va.mu.RLock()
	needsNewChunk := chunkID >= len(va.chunks)
	va.mu.RUnlock()

	if needsNewChunk {
		va.mu.Lock()
		// Double check under write lock
		for chunkID >= len(va.chunks) {
			if err := va.addChunk(len(va.chunks)); err != nil {
				va.mu.Unlock()
				return nil, err
			}
		}
		va.mu.Unlock()
	}

	va.mu.RLock()
	defer va.mu.RUnlock()

	chunk := va.chunks[chunkID]

	// Return a slice pointing directly to the OS-managed memory
	return chunk.Data[offset : offset+va.vectorSize], nil
}

func (va *VectorArena) Close() error {
	va.mu.Lock()
	defer va.mu.Unlock()

	var firstErr error
	for _, chunk := range va.chunks {
		if err := munmapFile(chunk.Data); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := chunk.File.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// --- ZERO-COPY CASTING HELPERS ---

// BytesToFloat32Slice casts a byte slice directly to a float32 slice without copying.
func BytesToFloat32Slice(b []byte, dim int) []float32 {
	if len(b) == 0 {
		return nil
	}
	return unsafe.Slice((*float32)(unsafe.Pointer(&b[0])), dim)
}

func BytesToUint16Slice(b []byte, dim int) []uint16 {
	if len(b) == 0 {
		return nil
	}
	return unsafe.Slice((*uint16)(unsafe.Pointer(&b[0])), dim)
}

func BytesToInt8Slice(b []byte, dim int) []int8 {
	if len(b) == 0 {
		return nil
	}
	return unsafe.Slice((*int8)(unsafe.Pointer(&b[0])), dim)
}
