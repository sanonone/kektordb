package mmap

import (
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type ArenaCompactionConfig struct {
	Enabled    bool          `json:"enabled"`
	Interval   time.Duration `json:"interval"`
	Threshold  float64       `json:"threshold"`
	BatchSize  int           `json:"batch_size"`
	BatchDelay time.Duration `json:"batch_delay"`
}

func DefaultArenaCompactionConfig() ArenaCompactionConfig {
	return ArenaCompactionConfig{
		Enabled:    true,
		Interval:   5 * time.Minute,
		Threshold:  0.3,
		BatchSize:  100,
		BatchDelay: 1 * time.Millisecond,
	}
}

type FragmentationStats struct {
	TotalPhysicalSlots int                  `json:"total_physical_slots"`
	UsedPhysicalSlots  int                  `json:"used_physical_slots"`
	FreePhysicalSlots  int                  `json:"free_physical_slots"`
	FragmentationRatio float64              `json:"fragmentation_ratio"`
	ChunkStats         []ChunkFragmentation `json:"chunk_stats"`
}

type ChunkFragmentation struct {
	ChunkID      int     `json:"chunk_id"`
	UsedSlots    int     `json:"used_slots"`
	FreeSlots    int     `json:"free_slots"`
	UsagePercent float64 `json:"usage_percent"`
}

type NodePointerUpdater interface {
	UpdateNodePointer(internalID uint32, newVectorBytes []byte)
}

type MaintenanceCoordinator interface {
	TryAcquireCompactionLock() bool
	ReleaseCompactionLock()
	TryAcquireSnapshotLock() bool
	ReleaseSnapshotLock()
	RecordWrite()
	IsWriteHeavy() bool
	ResetWriteCounter()
}

type AsyncCompactor struct {
	arena            *VectorArena
	config           ArenaCompactionConfig
	stopCh           chan struct{}
	stoppedCh        chan struct{} // Closed when compactor is fully stopped
	ticker           *time.Ticker
	mu               sync.Mutex
	running          bool
	nodeUpdater      NodePointerUpdater
	maintenanceCoord MaintenanceCoordinator
	lock             sync.Mutex
	wg               sync.WaitGroup
	stopped          atomic.Bool
	draining         atomic.Bool
}

func NewAsyncCompactor(arena *VectorArena, config ArenaCompactionConfig) *AsyncCompactor {
	if config.Interval == 0 {
		config.Interval = 5 * time.Minute
	}
	if config.Threshold == 0 {
		config.Threshold = 0.3
	}
	if config.BatchSize == 0 {
		config.BatchSize = 100
	}
	if config.BatchDelay == 0 {
		config.BatchDelay = 1 * time.Millisecond
	}

	return &AsyncCompactor{
		arena:     arena,
		config:    config,
		stopCh:    make(chan struct{}),
		stoppedCh: make(chan struct{}),
		ticker:    time.NewTicker(config.Interval),
		running:   false,
	}
}

func (ac *AsyncCompactor) SetMaintenanceCoordinator(coord MaintenanceCoordinator) {
	ac.maintenanceCoord = coord
}

func (ac *AsyncCompactor) SetNodeUpdater(updater NodePointerUpdater) {
	ac.nodeUpdater = updater
}

func (ac *AsyncCompactor) Start() {
	ac.mu.Lock()
	if ac.running {
		ac.mu.Unlock()
		return
	}
	ac.running = true
	ac.stopped.Store(false)
	ac.mu.Unlock()

	jitter := time.Duration(fastRandn(30000)) * time.Millisecond
	if jitter > 0 {
		time.Sleep(jitter)
	}

	ac.wg.Add(1)
	go ac.runLoop()
	slog.Info("[ArenaCompactor] Started", "interval", ac.config.Interval,
		"threshold", ac.config.Threshold, "jitter", jitter)
}

func (ac *AsyncCompactor) Stop() {
	ac.mu.Lock()
	if !ac.running {
		ac.mu.Unlock()
		return
	}

	slog.Info("[ArenaCompactor] Stop requested, setting draining flag")

	ac.draining.Store(true)

	if ac.stopped.CompareAndSwap(false, true) {
		close(ac.stopCh)
		ac.mu.Unlock()

		done := make(chan struct{})
		go func() {
			ac.wg.Wait()
			ac.ticker.Stop()
			close(done)
		}()

		select {
		case <-done:
			slog.Info("[ArenaCompactor] Stopped complete")
		case <-time.After(5 * time.Second):
			slog.Warn("[ArenaCompactor] Stop timed out after 5 seconds - goroutine may be blocked")
		}

		ac.mu.Lock()
		select {
		case <-ac.stoppedCh:
		default:
			close(ac.stoppedCh)
		}
		ac.running = false
		ac.mu.Unlock()
		return
	}
	ac.mu.Unlock()
}

// WaitForStopped waits for the compactor to be fully stopped.
// Returns immediately if the compactor is not running.
func (ac *AsyncCompactor) WaitForStopped() {
	ac.mu.Lock()
	if !ac.running {
		ac.mu.Unlock()
		return
	}
	ac.mu.Unlock()

	// Wait for the stopped channel to be closed
	<-ac.stoppedCh
}

func (ac *AsyncCompactor) runLoop() {
	defer ac.wg.Done()
	for {
		select {
		case <-ac.stopCh:
			slog.Debug("[ArenaCompactor] runLoop exiting due to stopCh closed")
			return
		case <-ac.ticker.C:
			// Skip if we're draining (shutdown in progress)
			if ac.draining.Load() {
				slog.Debug("[ArenaCompactor] Skipping - draining set")
				continue
			}
			ac.RunCycle()
		}
	}
}

func (ac *AsyncCompactor) RunCycle() {
	// Skip if we're draining (shutdown in progress)
	if ac.draining.Load() {
		slog.Debug("[ArenaCompactor] Skipping - draining (shutdown in progress)")
		return
	}

	if !ac.config.Enabled {
		return
	}

	if ac.maintenanceCoord != nil {
		if ac.maintenanceCoord.IsWriteHeavy() {
			slog.Debug("[ArenaCompactor] Skipping - high write activity")
			return
		}
		if !ac.maintenanceCoord.TryAcquireCompactionLock() {
			slog.Debug("[ArenaCompactor] Skipping - HNSW Vacuum running")
			return
		}
		defer ac.maintenanceCoord.ReleaseCompactionLock()
	}

	ac.lock.Lock()
	defer ac.lock.Unlock()

	// Double check draining after acquiring lock
	if ac.draining.Load() {
		slog.Debug("[ArenaCompactor] Skipping - draining flag set during lock acquisition")
		return
	}

	if !ac.config.Enabled {
		return
	}

	if ac.nodeUpdater == nil {
		slog.Debug("[ArenaCompactor] No node updater set, skipping compaction")
		return
	}

	stats := ac.analyzeFragmentation()

	if stats.FragmentationRatio < ac.config.Threshold {
		slog.Debug("[ArenaCompactor] Skipping - fragmentation below threshold",
			"fragmentation", stats.FragmentationRatio,
			"threshold", ac.config.Threshold)
		return
	}

	slog.Info("[ArenaCompactor] Starting compaction cycle",
		"fragmentation", stats.FragmentationRatio,
		"threshold", ac.config.Threshold,
		"total_slots", stats.TotalPhysicalSlots,
		"free_slots", stats.FreePhysicalSlots,
		"chunks_to_process", len(stats.ChunkStats))

	relocated := 0
	freed := 0

	// Compact each chunk - check draining after each chunk
	for _, cs := range stats.ChunkStats {
		// Check draining BEFORE processing each chunk
		if ac.draining.Load() {
			slog.Debug("[ArenaCompactor] Stopping chunk processing - draining set")
			break
		}

		if cs.UsedSlots == 0 {
			continue
		}
		slog.Debug("[ArenaCompactor] Processing chunk",
			"chunk_id", cs.ChunkID,
			"used_slots", cs.UsedSlots,
			"free_slots", cs.FreeSlots)
		chunkRelocated, chunkFreed := ac.compactChunk(cs.ChunkID)
		relocated += chunkRelocated
		freed += chunkFreed

		ac.tryDropEmptyChunks()

		// Check draining after each chunk
		if ac.draining.Load() {
			slog.Debug("[ArenaCompactor] Stopping after chunk - draining set")
			break
		}
	}

	slog.Info("[ArenaCompactor] Compaction cycle completed",
		"vectors_relocated", relocated,
		"slots_freed", freed,
		"chunks_remaining", len(ac.arena.chunks))
}

func (ac *AsyncCompactor) compactChunk(chunkID int) (relocated, freed int) {
	const batchSize = 100

	for {
		// Check draining BEFORE acquiring locks - this ensures we exit quickly during shutdown
		if ac.draining.Load() {
			return relocated, freed
		}

		select {
		case <-ac.stopCh:
			return relocated, freed
		default:
		}

		// Find active vectors in this chunk that can be moved
		// We want to move vectors from HIGH slots to LOW free slots
		batch := ac.identifyVectorsToMove(chunkID, batchSize)
		if len(batch) == 0 {
			break
		}

		// Read all vectors in batch
		vectors := make([]vectorData, len(batch))
		for i, internalID := range batch {
			physSlot := ac.arena.slotTable[internalID]
			if physSlot == UnallocatedSlot {
				continue
			}

			chunkIdx := int(physSlot) / ac.arena.vecsPerChk
			offset := ArenaHeaderSize + int(physSlot%uint32(ac.arena.vecsPerChk))*ac.arena.vectorSize

			vec := make([]byte, ac.arena.vectorSize)

			ac.arena.mu.RLock()
			if chunkIdx < len(ac.arena.chunks) && ac.arena.chunks[chunkIdx] != nil {
				copy(vec, ac.arena.chunks[chunkIdx].Data[offset:offset+ac.arena.vectorSize])
			}
			ac.arena.mu.RUnlock()

			vectors[i] = vectorData{
				internalID: internalID,
				fromSlot:   physSlot,
				data:       vec,
			}
		}

		// Find free slots - prefer LOW slots (for consolidation)
		newSlots := ac.arena.FindFreeSlots(len(batch))
		if len(newSlots) == 0 {
			break
		}

		// Move vectors to new positions
		ac.arena.slotMu.Lock()
		ac.arena.mu.Lock()

		for i, v := range vectors {
			if len(newSlots) <= i {
				break
			}

			// TOCTOU check
			if ac.arena.slotTable[v.internalID] != v.fromSlot {
				continue
			}

			targetSlot := newSlots[i]
			targetChunkID := int(targetSlot) / ac.arena.vecsPerChk
			targetOffset := ArenaHeaderSize + int(targetSlot%uint32(ac.arena.vecsPerChk))*ac.arena.vectorSize

			if targetChunkID < len(ac.arena.chunks) && ac.arena.chunks[targetChunkID] != nil {
				copy(ac.arena.chunks[targetChunkID].Data[targetOffset:targetOffset+ac.arena.vectorSize], v.data)
			}

			// Update slot table
			ac.arena.slotTable[v.internalID] = targetSlot

			// Update node pointer
			newBytes := ac.arena.chunks[targetChunkID].Data[targetOffset : targetOffset+ac.arena.vectorSize]
			if ac.nodeUpdater != nil {
				ac.nodeUpdater.UpdateNodePointer(v.internalID, newBytes)
			}

			relocated++
		}

		// Free old slots
		for i, v := range vectors {
			if len(newSlots) <= i {
				break
			}
			if ac.arena.slotTable[v.internalID] == v.fromSlot {
				continue
			}
			ac.arena.freeSlots = append(ac.arena.freeSlots, v.fromSlot)
			freed++
		}

		ac.arena.mu.Unlock()
		ac.arena.slotMu.Unlock()

		time.Sleep(ac.config.BatchDelay)
	}

	return relocated, freed
}

func (ac *AsyncCompactor) identifyVectorsToMove(chunkID, maxBatch int) []uint32 {
	ac.arena.slotMu.RLock()
	defer ac.arena.slotMu.RUnlock()

	chunkStartSlot := uint32(chunkID * ac.arena.vecsPerChk)
	chunkEndSlot := uint32((chunkID + 1) * ac.arena.vecsPerChk)

	// Get all free slots in this chunk - we'll move towards these
	freeSlotsInChunk := make(map[uint32]bool)
	for _, slot := range ac.arena.freeSlots {
		if slot >= chunkStartSlot && slot < chunkEndSlot {
			freeSlotsInChunk[slot] = true
		}
	}

	batch := make([]uint32, 0, maxBatch)

	// Find active vectors in this chunk that are AFTER some free slots
	// (meaning they can be moved to fill gaps)
	for i, physSlot := range ac.arena.slotTable {
		if physSlot == UnallocatedSlot {
			continue
		}

		if physSlot < chunkStartSlot || physSlot >= chunkEndSlot {
			continue
		}

		// Check if there's a free slot before this one
		hasFreeBefore := false
		for freeSlot := chunkStartSlot; freeSlot < physSlot; freeSlot++ {
			if freeSlotsInChunk[freeSlot] {
				hasFreeBefore = true
				break
			}
		}

		// Only move if there's free space before it
		if hasFreeBefore {
			batch = append(batch, uint32(i))
			if len(batch) >= maxBatch {
				break
			}
		}
	}

	return batch
}

func (ac *AsyncCompactor) tryDropEmptyChunks() {
	// Skip if we're draining (shutdown in progress)
	if ac.draining.Load() {
		return
	}

	// Lock ordering: slotMu -> mu (consistent with compactChunk)
	ac.arena.slotMu.Lock()
	defer ac.arena.slotMu.Unlock()

	ac.arena.mu.Lock()
	defer ac.arena.mu.Unlock()

	if len(ac.arena.chunks) == 0 {
		return
	}

	slog.Debug("[ArenaCompactor] Checking for empty chunks to drop",
		"total_chunks", len(ac.arena.chunks))

	droppedCount := 0
	for len(ac.arena.chunks) > 0 {
		lastChunkIdx := len(ac.arena.chunks) - 1
		chunk := ac.arena.chunks[lastChunkIdx]
		if chunk == nil {
			ac.arena.chunks = ac.arena.chunks[:lastChunkIdx]
			continue
		}

		chunkStartSlot := uint32(lastChunkIdx * ac.arena.vecsPerChk)
		chunkEndSlot := uint32((lastChunkIdx + 1) * ac.arena.vecsPerChk)

		isEmpty := true
		for _, slot := range ac.arena.slotTable {
			if slot != UnallocatedSlot && slot >= chunkStartSlot && slot < chunkEndSlot {
				isEmpty = false
				break
			}
		}

		if !isEmpty {
			break
		}

		slog.Info("[ArenaCompactor] Dropping empty chunk", "chunk", lastChunkIdx)

		droppedCount++

		if err := munmapFile(chunk.Data); err != nil {
			slog.Warn("[ArenaCompactor] Failed to unmap chunk", "chunk", lastChunkIdx, "error", err)
		}

		if err := chunk.File.Close(); err != nil {
			slog.Warn("[ArenaCompactor] Failed to close chunk file", "chunk", lastChunkIdx, "error", err)
		}

		filePath := chunk.File.Name()
		if err := os.Remove(filePath); err != nil {
			slog.Warn("[ArenaCompactor] Failed to delete chunk file", "chunk", lastChunkIdx, "error", err)
		} else {
			slog.Info("[ArenaCompactor] Physically deleted chunk file", "chunk", lastChunkIdx, "path", filePath)
		}

		ac.arena.chunks = ac.arena.chunks[:lastChunkIdx]
	}

	if droppedCount > 0 {
		slog.Info("[ArenaCompactor] Dropped empty chunks",
			"dropped", droppedCount,
			"chunks_remaining", len(ac.arena.chunks))
	}
}

func (ac *AsyncCompactor) analyzeFragmentation() FragmentationStats {
	ac.arena.slotMu.RLock()
	usedSlots := 0
	for _, slot := range ac.arena.slotTable {
		if slot != UnallocatedSlot {
			usedSlots++
		}
	}
	ac.arena.slotMu.RUnlock()

	ac.arena.mu.RLock()
	totalChunks := len(ac.arena.chunks)
	ac.arena.mu.RUnlock()

	totalPhysicalSlots := totalChunks * ac.arena.vecsPerChk

	chunkStats := ac.getChunkStats()

	usedPhysicalSlots := 0
	for _, cs := range chunkStats {
		usedPhysicalSlots += cs.UsedSlots
	}

	freePhysicalSlots := totalPhysicalSlots - usedPhysicalSlots
	var ratio float64
	if totalPhysicalSlots > 0 {
		ratio = float64(freePhysicalSlots) / float64(totalPhysicalSlots)
	}

	return FragmentationStats{
		TotalPhysicalSlots: totalPhysicalSlots,
		UsedPhysicalSlots:  usedPhysicalSlots,
		FreePhysicalSlots:  freePhysicalSlots,
		FragmentationRatio: ratio,
		ChunkStats:         chunkStats,
	}
}

func (ac *AsyncCompactor) getChunkStats() []ChunkFragmentation {
	ac.arena.mu.RLock()
	defer ac.arena.mu.RUnlock()

	ac.arena.slotMu.RLock()
	defer ac.arena.slotMu.RUnlock()

	stats := make([]ChunkFragmentation, 0, len(ac.arena.chunks))

	for chunkIdx := range ac.arena.chunks {
		if ac.arena.chunks[chunkIdx] == nil {
			continue
		}

		startSlot := uint32(chunkIdx * ac.arena.vecsPerChk)
		endSlot := uint32((chunkIdx + 1) * ac.arena.vecsPerChk)

		usedCount := 0

		for _, physSlot := range ac.arena.slotTable {
			if physSlot != UnallocatedSlot && physSlot >= startSlot && physSlot < endSlot {
				usedCount++
			}
		}

		stats = append(stats, ChunkFragmentation{
			ChunkID:      chunkIdx,
			UsedSlots:    usedCount,
			FreeSlots:    ac.arena.vecsPerChk - usedCount,
			UsagePercent: float64(usedCount) / float64(ac.arena.vecsPerChk),
		})
	}

	return stats
}

type vectorData struct {
	internalID uint32
	fromSlot   uint32
	data       []byte
}

// fastRandn returns a fast pseudo-random int in [0, n)
func fastRandn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(time.Now().UnixNano() % int64(n))
}
