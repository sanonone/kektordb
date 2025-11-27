// Package engine provides the high-level, embedded interface for KektorDB.
//
// It orchestrates the in-memory vector data structures (Core) and the on-disk
// persistence layer (AOF/Snapshot), providing a thread-safe database instance
// that can be used directly within Go applications without network overhead.
//
// Basic usage:
//
//	opts := engine.DefaultOptions("./data")
//	db, err := engine.Open(opts)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer db.Close()
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

// Options configures the behavior of the Engine, including persistence paths
// and automatic maintenance policies.
type Options struct {
	// DataDir is the directory where .aof and .kdb files will be stored.
	// It is created automatically if it does not exist.
	DataDir string

	// AofFilename is the name of the Append-Only File (default: "kektordb.aof").
	// The snapshot file will effectively be named <AofFilename>.kdb.
	AofFilename string

	// AutoSaveInterval defines how much time must pass since the last save
	// before a new snapshot is triggered (if AutoSaveThreshold is also met).
	// Set to 0 to disable auto-saving based on time.
	AutoSaveInterval time.Duration

	// AutoSaveThreshold defines how many write operations must occur
	// before a new snapshot is triggered (if AutoSaveInterval is also met).
	// Set to 0 to disable auto-saving based on write count.
	AutoSaveThreshold int64

	// AofRewritePercentage triggers an automatic AOF compaction (rewrite) when the
	// AOF file size exceeds the base size by this percentage.
	// E.g., 100 means rewrite when size doubles. Set to 0 to disable.
	AofRewritePercentage int
}

// DefaultOptions returns a standard configuration suitable for most use cases.
//
// Defaults:
//   - DataDir: provided path
//   - AofFilename: "kektordb.aof"
//   - AutoSave: Every 60s if at least 1000 changes occurred
//   - AofRewrite: At 100% growth
func DefaultOptions(dataDir string) Options {
	return Options{
		DataDir:              dataDir,
		AofFilename:          "kektordb.aof",
		AutoSaveInterval:     60 * time.Second,
		AutoSaveThreshold:    1000,
		AofRewritePercentage: 100,
	}
}

// Engine is the main entry point for KektorDB.
// It coordinates the in-memory Core and the on-disk Persistence.
//
// Use Open() to initialize an Engine and Close() to shut it down gracefully.
type Engine struct {
	// DB is the underlying in-memory core.
	// While exported, it is recommended to use Engine methods (e.g., VAdd, VSearch)
	// to ensure operations are correctly persisted to disk.
	DB *core.DB

	// AOF handles the append-only log.
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

// Open initializes a new Engine instance using the provided options.
//
// It performs the following actions:
// 1. Creates DataDir if missing.
// 2. Loads the latest Snapshot (.kdb) if available.
// 3. Replays the AOF (.aof) to recover recent data.
// 4. Starts background goroutines for auto-saving and compaction.
//
// This method blocks until the database is fully loaded and ready.
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

// Close performs a clean shutdown of the Engine.
//
// It stops background maintenance tasks and closes the AOF file.
// Note: It does not force a final snapshot, but all data is already persisted
// in the AOF file, ensuring durability on restart.
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
// (Unexported: internal use only)
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

// checkMaintenance evaluates if a snapshot or AOF rewrite is needed.
// (Unexported: internal use only)
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
