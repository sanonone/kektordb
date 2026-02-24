package mmap

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"unsafe"
)

const (
	// DefaultChunkSize is 64MB.
	DefaultChunkSize = 64 * 1024 * 1024
	ArenaMagic       = 0x4B414F4E // "KARN"
	ArenaVersion     = 1
	ArenaHeaderSize  = 64
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
	dim        uint32
	precision  uint8
}

// Precision constants (mapped from distance types for storage)
const (
	PrecFloat32 uint8 = 0
	PrecFloat16 uint8 = 1
	PrecInt8    uint8 = 2
)

// NewVectorArena initializes the arena.
// vectorSize is the size in bytes (e.g., dim * 4 for Float32).
func NewVectorArena(dir string, vectorSize int, dim int, precision uint8) (*VectorArena, error) {
	if vectorSize <= 0 {
		return nil, fmt.Errorf("vectorSize must be > 0")
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create arena dir: %w", err)
	}

	// Calcoliamo quanti vettori ci stanno DOPO aver sottratto l'header di 64 byte
	availableSpace := DefaultChunkSize - ArenaHeaderSize
	vecsPerChk := availableSpace / vectorSize
	if vecsPerChk == 0 {
		return nil, fmt.Errorf("vector size %d exceeds chunk payload capacity", vectorSize)
	}

	va := &VectorArena{
		dir:        dir,
		chunkSize:  DefaultChunkSize,
		vectorSize: vectorSize,
		vecsPerChk: vecsPerChk,
		chunks:     make([]*Chunk, 0),
		dim:        uint32(dim),
		precision:  precision,
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

	info, err := file.Stat()
	if err != nil {
		return err
	}

	isNewFile := info.Size() == 0

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

	// --- HEADER MANAGEMENT ---
	if isNewFile {
		// Scrivi header nel nuovo file direttamente in memoria
		binary.LittleEndian.PutUint32(data[0:4], ArenaMagic)
		binary.LittleEndian.PutUint32(data[4:8], ArenaVersion)
		binary.LittleEndian.PutUint32(data[8:12], va.dim)
		data[12] = va.precision
		// I restanti byte fino a 64 rimangono 0 (Reserved)
	} else {
		// Valida header file esistente
		magic := binary.LittleEndian.Uint32(data[0:4])
		version := binary.LittleEndian.Uint32(data[4:8])
		fileDim := binary.LittleEndian.Uint32(data[8:12])
		filePrec := data[12]

		if magic != ArenaMagic {
			return fmt.Errorf("file %s is not a valid arena (magic mismatch)", fileName)
		}
		if version != ArenaVersion {
			return fmt.Errorf("file %s unsupported version %d", fileName, version)
		}
		if fileDim != va.dim {
			return fmt.Errorf("file %s dimension mismatch: expected %d, got %d", fileName, va.dim, fileDim)
		}
		if filePrec != va.precision {
			return fmt.Errorf("file %s precision mismatch: expected %d, got %d", fileName, va.precision, filePrec)
		}
	}
	// -------------------------

	chunk := &Chunk{
		ID:   chunkID,
		File: file,
		Data: data,
	}

	va.chunks = append(va.chunks, chunk)
	return nil
}

// GetBytes returns a slice of bytes pointing to the memory-mapped region for the given ID.
func (va *VectorArena) GetBytes(internalID uint32) ([]byte, error) {
	chunkID := int(internalID) / va.vecsPerChk
	offset := ArenaHeaderSize + (int(internalID)%va.vecsPerChk)*va.vectorSize

	// --- FAST PATH (Read Only) ---
	// If the chunk already exists, we read it and exit immediately.
	// This allows thousands of goroutines to read at once.
	va.mu.RLock()
	if chunkID < len(va.chunks) {
		chunk := va.chunks[chunkID]
		va.mu.RUnlock()
		return chunk.Data[offset : offset+va.vectorSize], nil
	}
	va.mu.RUnlock()

	// --- SLOW PATH (Write) ---
	// The chunk doesn't exist, we need to create it.
	// We acquire the exclusive lock to prevent two goroutines from creating it together.
	va.mu.Lock()
	defer va.mu.Unlock()

	// Double-check: another goroutine might have created this while we were waiting for the Lock
	for chunkID >= len(va.chunks) {
		if err := va.addChunk(len(va.chunks)); err != nil {
			return nil, err
		}
	}

	chunk := va.chunks[chunkID]
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
