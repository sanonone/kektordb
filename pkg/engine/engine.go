package engine

import (
	"fmt"
	"github.com/sanonone/kektordb/pkg/core"
	"github.com/sanonone/kektordb/pkg/persistence"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Options configures the Engine.
type Options struct {
	DataDir              string // Directory to store .aof and .kdb files
	AofFilename          string // Default: "kektordb.aof"
	AutoSaveInterval     time.Duration
	AutoSaveThreshold    int64 // Number of writes before triggering save
	AofRewritePercentage int   // 0 to disable
}

// DefaultOptions returns standard defaults.
func DefaultOptions(dataDir string) Options {
	return Options{
		DataDir:              dataDir,
		AofFilename:          "kektordb.aof",
		AutoSaveInterval:     60 * time.Second,
		AutoSaveThreshold:    1000,
		AofRewritePercentage: 100,
	}
}

// Engine is the high-level controller for KektorDB.
// It coordinates the in-memory Core and the on-disk Persistence.
type Engine struct {
	DB  *core.DB
	AOF *persistence.AOFWriter

	opts        Options
	aofPath     string
	snapPath    string
	aofBaseSize int64

	// DirtyCounter tracks the number of write operations since the last save.
	dirtyCounter int64
	lastSaveTime time.Time

	// Mutex for Engine-level administrative tasks (like Rewrite/Save)
	// Note: core.DB has its own internal granular locks for data access.
	adminMu sync.Mutex

	closed chan struct{}
	wg     sync.WaitGroup
}

// Open initializes the database engine.
// It loads existing data from snapshot and AOF, and starts background maintenance tasks.
func Open(opts Options) (*Engine, error) {
	if err := os.MkdirAll(opts.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	aofPath := filepath.Join(opts.DataDir, opts.AofFilename)
	snapPath := strings.TrimSuffix(aofPath, filepath.Ext(aofPath)) + ".kdb"

	e := &Engine{
		DB:           core.NewDB(),
		opts:         opts,
		aofPath:      aofPath,
		snapPath:     snapPath,
		lastSaveTime: time.Now(),
		closed:       make(chan struct{}),
	}

	// 1. Load Snapshot if exists
	if _, err := os.Stat(snapPath); err == nil {
		f, err := os.Open(snapPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open snapshot: %w", err)
		}
		defer f.Close()
		if err := e.DB.LoadFromSnapshot(f); err != nil {
			return nil, fmt.Errorf("failed to load snapshot: %w", err)
		}
	}

	// 2. Open AOF
	aofWriter, err := persistence.NewAOFWriter(aofPath)
	if err != nil {
		return nil, err
	}
	e.AOF = aofWriter

	// 3. Replay AOF (Recover missing data)
	// This reads the AOF and applies changes to the DB in-memory
	if err := e.replayAOF(); err != nil {
		e.AOF.Close()
		return nil, fmt.Errorf("failed to replay AOF: %w", err)
	}

	// Record AOF size for Rewrite logic
	info, _ := e.AOF.File().Stat()
	e.aofBaseSize = info.Size()

	// 4. Start Background Tasks
	e.wg.Add(1)
	go e.backgroundTasks()

	return e, nil
}

// Close performs a clean shutdown, syncing data and stopping background tasks.
func (e *Engine) Close() error {
	close(e.closed)
	e.wg.Wait() // Wait for background tasks to finish

	// Final sync
	if e.AOF != nil {
		return e.AOF.Close()
	}
	return nil
}

// backgroundTasks handles automatic saving and AOF rewriting.
func (e *Engine) backgroundTasks() {
	defer e.wg.Done()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-e.closed:
			return
		case <-ticker.C:
			e.checkMaintenance()
		}
	}
}

func (e *Engine) checkMaintenance() {
	// Lightweight atomic check
	dirty := atomic.LoadInt64(&e.dirtyCounter)

	// Auto-Save Policy
	if e.opts.AutoSaveThreshold > 0 && e.opts.AutoSaveInterval > 0 {
		if dirty >= e.opts.AutoSaveThreshold && time.Since(e.lastSaveTime) >= e.opts.AutoSaveInterval {
			// We ignore errors in background loop, but log them in a real app
			_ = e.SaveSnapshot()
		}
	}

	// AOF Rewrite Policy
	if e.opts.AofRewritePercentage > 0 {
		info, err := e.AOF.File().Stat()
		if err == nil {
			currentSize := info.Size()
			threshold := e.aofBaseSize + (e.aofBaseSize * int64(e.opts.AofRewritePercentage) / 100)
			// Min threshold 1MB to avoid rewriting tiny files constantly
			if threshold < 1024*1024 {
				threshold = 1024 * 1024
			}

			if e.aofBaseSize > 0 && currentSize > threshold {
				_ = e.RewriteAOF()
			}
		}
	}
}
