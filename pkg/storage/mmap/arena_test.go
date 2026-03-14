package mmap

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestVectorArena_BasicAllocFree(t *testing.T) {
	tmpDir := t.TempDir()

	arena, err := NewVectorArena(tmpDir, 16*4, 16, PrecFloat32)
	if err != nil {
		t.Fatalf("Failed to create arena: %v", err)
	}
	defer arena.Close()

	if arena.nextPhysSlot != 0 {
		t.Errorf("Expected nextPhysSlot to be 0, got %d", arena.nextPhysSlot)
	}

	slots := make([]uint32, 10)
	for i := 0; i < 10; i++ {
		slot, err := arena.AllocSlot(uint32(i))
		if err != nil {
			t.Fatalf("Failed to alloc slot %d: %v", i, err)
		}
		slots[i] = slot
	}

	for i := 0; i < 10; i++ {
		if slots[i] != uint32(i) {
			t.Errorf("Expected slot %d, got %d", i, slots[i])
		}
	}

	arena.FreeSlot(2)
	arena.FreeSlot(5)
	arena.FreeSlot(7)

	if len(arena.freeSlots) != 3 {
		t.Errorf("Expected 3 free slots, got %d", len(arena.freeSlots))
	}

	newSlot, err := arena.AllocSlot(10)
	if err != nil {
		t.Fatalf("Failed to alloc slot after free: %v", err)
	}

	if newSlot != 7 {
		t.Errorf("Expected to reuse slot 7, got %d", newSlot)
	}
}

func TestVectorArena_GetBytes(t *testing.T) {
	tmpDir := t.TempDir()

	dim := 128
	arena, err := NewVectorArena(tmpDir, dim*4, dim, PrecFloat32)
	if err != nil {
		t.Fatalf("Failed to create arena: %v", err)
	}
	defer arena.Close()

	_, err = arena.AllocSlot(0)
	if err != nil {
		t.Fatalf("Failed to alloc slot: %v", err)
	}

	bytes, err := arena.GetBytes(0)
	if err != nil {
		t.Fatalf("Failed to get bytes: %v", err)
	}

	vec := BytesToFloat32Slice(bytes, dim)

	rng := rand.New(rand.NewSource(42))
	for i := 0; i < dim; i++ {
		vec[i] = rng.Float32()
	}

	readBack, err := arena.GetBytes(0)
	if err != nil {
		t.Fatalf("Failed to read back bytes: %v", err)
	}

	vecRead := BytesToFloat32Slice(readBack, dim)

	for i := 0; i < dim; i++ {
		if vecRead[i] != vec[i] {
			t.Errorf("Data mismatch at index %d: expected %f, got %f", i, vec[i], vecRead[i])
		}
	}
}

func TestVectorArena_Persistence(t *testing.T) {
	tmpDir := t.TempDir()

	dim := 64
	originalArena, err := NewVectorArena(tmpDir, dim*4, dim, PrecFloat32)
	if err != nil {
		t.Fatalf("Failed to create original arena: %v", err)
	}

	for i := 0; i < 100; i++ {
		_, err := originalArena.AllocSlot(uint32(i))
		if err != nil {
			t.Fatalf("Failed to alloc slot %d: %v", i, err)
		}

		bytes, err := originalArena.GetBytes(uint32(i))
		if err != nil {
			t.Fatalf("Failed to get bytes for %d: %v", i, err)
		}

		vec := BytesToFloat32Slice(bytes, dim)
		for j := 0; j < dim; j++ {
			vec[j] = float32(i*dim + j)
		}
	}

	state := originalArena.GetState()
	if len(state.SlotTable) != 100 {
		t.Errorf("Expected 100 slots, got %d", len(state.SlotTable))
	}

	originalArena.Close()

	restoredArena, err := NewVectorArena(tmpDir, dim*4, dim, PrecFloat32)
	if err != nil {
		t.Fatalf("Failed to create restored arena: %v", err)
	}
	defer restoredArena.Close()

	restoredArena.LoadState(state)

	if len(restoredArena.slotTable) != 100 {
		t.Errorf("Expected 100 slots after restore, got %d", len(restoredArena.slotTable))
	}

	for i := 0; i < 100; i++ {
		bytes, err := restoredArena.GetBytes(uint32(i))
		if err != nil {
			t.Fatalf("Failed to get bytes after restore for %d: %v", i, err)
		}

		vec := BytesToFloat32Slice(bytes, dim)
		for j := 0; j < dim; j++ {
			expected := float32(i*dim + j)
			if vec[j] != expected {
				t.Errorf("Data mismatch at vector %d, index %d: expected %f, got %f",
					i, j, expected, vec[j])
			}
		}
	}
}

func TestVectorArena_FindFreeSlots(t *testing.T) {
	tmpDir := t.TempDir()

	arena, err := NewVectorArena(tmpDir, 16*4, 16, PrecFloat32)
	if err != nil {
		t.Fatalf("Failed to create arena: %v", err)
	}
	defer arena.Close()

	freeSlots := arena.FindFreeSlots(5)
	if len(freeSlots) != 5 {
		t.Errorf("Expected 5 new slots, got %d", len(freeSlots))
	}

	arena.FreeSlot(0)
	arena.FreeSlot(2)
	arena.FreeSlot(4)

	freeSlots = arena.FindFreeSlots(2)
	if len(freeSlots) != 2 {
		t.Errorf("Expected 2 slots (1 new + 1 reused), got %d", len(freeSlots))
	}
}

func TestVectorArena_FragmentationStats(t *testing.T) {
	tmpDir := t.TempDir()

	arena, err := NewVectorArena(tmpDir, 16*4, 16, PrecFloat32)
	if err != nil {
		t.Fatalf("Failed to create arena: %v", err)
	}
	defer arena.Close()

	for i := 0; i < 50; i++ {
		arena.AllocSlot(uint32(i))
		bytes, _ := arena.GetBytes(uint32(i))
		_ = bytes
	}

	arena.FreeSlot(10)
	arena.FreeSlot(20)
	arena.FreeSlot(30)

	compactor := NewAsyncCompactor(arena, DefaultArenaCompactionConfig())
	arena.compactor = compactor
	stats := arena.GetFragmentationStats()

	if stats.TotalPhysicalSlots == 0 {
		t.Error("Expected non-zero total physical slots")
	}
}

func TestAsyncCompactor_StartStop(t *testing.T) {
	tmpDir := t.TempDir()

	arena, err := NewVectorArena(tmpDir, 16*4, 16, PrecFloat32)
	if err != nil {
		t.Fatalf("Failed to create arena: %v", err)
	}
	defer arena.Close()

	cfg := DefaultArenaCompactionConfig()
	cfg.Interval = 100 * time.Millisecond
	cfg.Threshold = 0.1
	cfg.BatchSize = 10

	compactor := NewAsyncCompactor(arena, cfg)
	compactor.Start()

	time.Sleep(200 * time.Millisecond)
	compactor.Stop()

	time.Sleep(50 * time.Millisecond)
}

func TestAsyncCompactor_RelocatesVectors(t *testing.T) {
	tmpDir := t.TempDir()

	arena, err := NewVectorArena(tmpDir, 16*4, 16, PrecFloat32)
	if err != nil {
		t.Fatalf("Failed to create arena: %v", err)
	}

	// Add vectors with data
	numVectors := 100
	for i := 0; i < numVectors; i++ {
		arena.AllocSlot(uint32(i))
		bytes, _ := arena.GetBytes(uint32(i))
		vec := BytesToFloat32Slice(bytes, 16)
		vec[0] = float32(i) // Mark with ID
	}

	// Free first 80 to create fragmentation
	for i := 0; i < 80; i++ {
		arena.FreeSlot(uint32(i))
	}

	// Start compactor with callback
	cfg := DefaultArenaCompactionConfig()
	cfg.Threshold = 0.3
	cfg.BatchSize = 20
	cfg.Interval = 50 * time.Millisecond

	// Create a mock updater that tracks updates
	updatedIDs := &sync.Map{}
	mockUpdater := &mockNodeUpdater{updatedIDs: updatedIDs}

	compactor := NewAsyncCompactor(arena, cfg)
	compactor.SetNodeUpdater(mockUpdater)
	compactor.Start()

	// Wait for compaction
	time.Sleep(500 * time.Millisecond)
	compactor.Stop()

	// Check that some vectors were relocated
	count := 0
	updatedIDs.Range(func(key, value interface{}) bool {
		count++
		return true
	})

	t.Logf("Vectors relocated: %d", count)

	// Verify data is still correct for non-freed vectors
	for i := 80; i < numVectors; i++ {
		bytes, err := arena.GetBytes(uint32(i))
		if err != nil {
			t.Errorf("Failed to get bytes for vector %d: %v", i, err)
			continue
		}
		vec := BytesToFloat32Slice(bytes, 16)
		if vec[0] != float32(i) {
			t.Errorf("Data corruption! Vector %d: expected %f, got %f", i, float32(i), vec[0])
		}
	}
}

type mockNodeUpdater struct {
	updatedIDs *sync.Map
}

func (m *mockNodeUpdater) UpdateNodePointer(internalID uint32, newVectorBytes []byte) {
	m.updatedIDs.Store(internalID, true)
}

func TestAsyncCompactor_FragmentationAfterCompaction(t *testing.T) {
	tmpDir := t.TempDir()

	arena, err := NewVectorArena(tmpDir, 16*4, 16, PrecFloat32)
	if err != nil {
		t.Fatalf("Failed to create arena: %v", err)
	}
	defer arena.Close()

	// Add 100 vectors
	for i := 0; i < 100; i++ {
		arena.AllocSlot(uint32(i))
		bytes, _ := arena.GetBytes(uint32(i))
		_ = bytes
	}

	// Free 80% - high fragmentation
	for i := 0; i < 80; i++ {
		arena.FreeSlot(uint32(i))
	}

	// Get initial fragmentation
	compactor := NewAsyncCompactor(arena, DefaultArenaCompactionConfig())
	arena.compactor = compactor

	initialStats := arena.GetFragmentationStats()
	t.Logf("Initial fragmentation: %f", initialStats.FragmentationRatio)

	// Set up mock updater
	updatedIDs := &sync.Map{}
	mockUpdater := &mockNodeUpdater{updatedIDs: updatedIDs}

	cfg := DefaultArenaCompactionConfig()
	cfg.Threshold = 0.3
	cfg.Interval = 50 * time.Millisecond
	cfg.BatchSize = 10
	cfg.InitialDelay = 0 // Use deterministic behavior for test - no random jitter

	compactor = NewAsyncCompactor(arena, cfg)
	compactor.SetNodeUpdater(mockUpdater)
	compactor.Start()

	time.Sleep(500 * time.Millisecond)
	compactor.Stop()

	finalStats := arena.GetFragmentationStats()
	t.Logf("Final fragmentation: %f", finalStats.FragmentationRatio)

	// Check some vectors were moved
	count := 0
	updatedIDs.Range(func(key, value interface{}) bool {
		count++
		return true
	})

	t.Logf("Vectors relocated: %d", count)

	if count == 0 {
		t.Error("Expected some vectors to be relocated")
	}
}

func TestAsyncCompactor_ConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()

	dim := 16
	arena, err := NewVectorArena(tmpDir, dim*4, dim, PrecFloat32)
	if err != nil {
		t.Fatalf("Failed to create arena: %v", err)
	}
	defer arena.Close()

	cfg := DefaultArenaCompactionConfig()
	cfg.Threshold = 0.2
	cfg.BatchSize = 20
	cfg.Interval = 50 * time.Millisecond

	compactor := NewAsyncCompactor(arena, cfg)
	compactor.SetNodeUpdater(&mockNodeUpdater{updatedIDs: &sync.Map{}})
	compactor.Start()

	var wg sync.WaitGroup

	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				id := gid*100 + i
				arena.AllocSlot(uint32(id))
				bytes, _ := arena.GetBytes(uint32(id))
				vec := BytesToFloat32Slice(bytes, dim)
				if len(vec) > 0 {
					vec[0] = float32(id)
				}
			}
		}(g)
	}

	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				id := gid*100 + i
				arena.FreeSlot(uint32(id))
			}
		}(g)
	}

	time.Sleep(500 * time.Millisecond)
	compactor.Stop()

	wg.Wait()

	arena.slotMu.RLock()
	totalSlots := len(arena.slotTable)
	arena.slotMu.RUnlock()

	t.Logf("Total slots after concurrent test: %d", totalSlots)
}

func TestAsyncCompactor_LockFreeReadsDuringCompaction(t *testing.T) {
	tmpDir := t.TempDir()

	dim := 64
	arena, err := NewVectorArena(tmpDir, dim*4, dim, PrecFloat32)
	if err != nil {
		t.Fatalf("Failed to create arena: %v", err)
	}
	defer arena.Close()

	for i := 0; i < 200; i++ {
		arena.AllocSlot(uint32(i))
		bytes, _ := arena.GetBytes(uint32(i))
		vec := BytesToFloat32Slice(bytes, dim)
		if len(vec) > 0 {
			vec[0] = float32(i)
		}
	}

	for i := 0; i < 180; i++ {
		arena.FreeSlot(uint32(i))
	}

	cfg := DefaultArenaCompactionConfig()
	cfg.Threshold = 0.3
	cfg.BatchSize = 10
	cfg.Interval = 20 * time.Millisecond

	compactor := NewAsyncCompactor(arena, cfg)
	compactor.SetNodeUpdater(&mockNodeUpdater{updatedIDs: &sync.Map{}})
	compactor.Start()

	var readOps atomic.Int64
	var wg sync.WaitGroup

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				for j := 180; j < 200; j++ {
					_, err := arena.GetBytes(uint32(j))
					if err == nil {
						readOps.Add(1)
					}
				}
				time.Sleep(100 * time.Microsecond)
			}
		}()
	}

	wg.Wait()
	compactor.Stop()

	t.Logf("Successful read operations during compaction: %d", readOps.Load())
}

func TestVectorArena_ConcurrentAllocFree(t *testing.T) {
	tmpDir := t.TempDir()

	arena, err := NewVectorArena(tmpDir, 16*4, 16, PrecFloat32)
	if err != nil {
		t.Fatalf("Failed to create arena: %v", err)
	}
	defer arena.Close()

	var wg sync.WaitGroup
	var successfulAllocs atomic.Int64
	var successfulFrees atomic.Int64

	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				id := gid*1000 + i
				_, err := arena.AllocSlot(uint32(id))
				if err == nil {
					successfulAllocs.Add(1)
				}
			}
		}(g)
	}

	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				id := gid*1000 + i
				arena.FreeSlot(uint32(id))
				successfulFrees.Add(1)
			}
		}(g)
	}

	wg.Wait()

	t.Logf("Successful allocs: %d, successful frees: %d",
		successfulAllocs.Load(), successfulFrees.Load())

	_, err = arena.AllocSlot(10000)
	if err != nil {
		t.Errorf("Arena not functional after concurrent access: %v", err)
	}
}

func TestVectorArena_ZeroVector(t *testing.T) {
	tmpDir := t.TempDir()

	dim := 8
	arena, err := NewVectorArena(tmpDir, dim*4, dim, PrecFloat32)
	if err != nil {
		t.Fatalf("Failed to create arena: %v", err)
	}
	defer arena.Close()

	arena.AllocSlot(0)

	bytes, err := arena.GetBytes(0)
	if err != nil {
		t.Fatalf("Failed to get bytes: %v", err)
	}

	vec := BytesToFloat32Slice(bytes, dim)

	for i := 0; i < dim; i++ {
		if vec[i] != 0 {
			t.Errorf("Expected zero at index %d, got %f", i, vec[i])
		}
	}
}

func TestVectorArena_OutOfBounds(t *testing.T) {
	tmpDir := t.TempDir()

	arena, err := NewVectorArena(tmpDir, 16*4, 16, PrecFloat32)
	if err != nil {
		t.Fatalf("Failed to create arena: %v", err)
	}
	defer arena.Close()

	arena.AllocSlot(0)

	_, err = arena.GetBytes(1)
	if err == nil {
		t.Error("Expected error for non-allocated slot")
	}

	arena.FreeSlot(0)

	_, err = arena.GetBytes(0)
	if err == nil {
		t.Error("Expected error for freed slot")
	}
}

func TestVectorArena_MultipleChunks(t *testing.T) {
	tmpDir := t.TempDir()

	dim := 4
	arena, err := NewVectorArena(tmpDir, dim*4, dim, PrecFloat32)
	if err != nil {
		t.Fatalf("Failed to create arena: %v", err)
	}
	defer arena.Close()

	vecsPerChk := arena.vecsPerChk

	for i := 0; i < vecsPerChk+10; i++ {
		slot, err := arena.AllocSlot(uint32(i))
		if err != nil {
			t.Fatalf("Failed to alloc slot %d: %v", i, err)
		}
		expectedChunk := i / vecsPerChk
		actualChunk := int(slot) / vecsPerChk
		if expectedChunk != actualChunk {
			t.Errorf("Chunk mismatch for slot %d: expected %d, got %d", slot, expectedChunk, actualChunk)
		}
	}
}

func TestVectorArena_PrecisionTypes(t *testing.T) {
	testCases := []struct {
		name string
		prec uint8
		dim  int
	}{
		{"Float32", PrecFloat32, 128},
		{"Float16", PrecFloat16, 128},
		{"Int8", PrecInt8, 128},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			vecSize := tc.dim
			if tc.prec == PrecFloat16 {
				vecSize = tc.dim * 2
			} else if tc.prec == PrecInt8 {
				vecSize = tc.dim
			}

			arena, err := NewVectorArena(tmpDir, vecSize, tc.dim, tc.prec)
			if err != nil {
				t.Fatalf("Failed to create arena: %v", err)
			}
			defer arena.Close()

			slot, err := arena.AllocSlot(0)
			if err != nil {
				t.Fatalf("Failed to alloc: %v", err)
			}
			if slot != 0 {
				t.Errorf("Expected slot 0, got %d", slot)
			}
		})
	}
}

func BenchmarkVectorArena_AllocFree(b *testing.B) {
	tmpDir := b.TempDir()
	arena, _ := NewVectorArena(tmpDir, 16*4, 16, PrecFloat32)
	defer arena.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		slot, _ := arena.AllocSlot(uint32(i))
		arena.FreeSlot(slot)
	}
}

func BenchmarkVectorArena_Concurrent(b *testing.B) {
	tmpDir := b.TempDir()
	arena, _ := NewVectorArena(tmpDir, 16*4, 16, PrecFloat32)
	defer arena.Close()

	var wg sync.WaitGroup
	b.ResetTimer()

	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < b.N/10; i++ {
				id := gid*10000 + i
				arena.AllocSlot(uint32(id))
			}
		}(g)
	}

	wg.Wait()
}
