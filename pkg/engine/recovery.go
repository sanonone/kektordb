package engine

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sanonone/kektordb/pkg/core"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/persistence"
)

// replayAOF reads the AOF file and reconstructs the state.
// It handles the logic of compacting commands (loading into a map first).
func (e *Engine) replayAOF() error {
	file, err := os.Open(e.aofPath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Temporary state structures for compaction
	type vectorEntry struct {
		vector   []float32
		metadata map[string]any
	}
	type indexState struct {
		metric         distance.DistanceMetric
		m              int
		efConstruction int
		precision      distance.PrecisionType
		textLanguage   string
		entries        map[string]vectorEntry
	}

	kvData := make(map[string][]byte)
	indexes := make(map[string]*indexState)

	reader := bufio.NewReader(file)

	// Phase 1: Read and Aggregate (Compaction logic)
	for {
		cmd, err := persistence.ParseCommand(reader)
		if err == io.EOF {
			break
		}
		if err != nil {
			// Log error but try to continue or break?
			// For robustness we assume the file might have a partial write at the end.
			break
		}

		switch cmd.Name {
		case "SET":
			if len(cmd.Args) == 2 {
				kvData[string(cmd.Args[0])] = cmd.Args[1]
			}
		case "DEL":
			if len(cmd.Args) == 1 {
				delete(kvData, string(cmd.Args[0]))
			}
		case "VDROP":
			if len(cmd.Args) == 1 {
				delete(indexes, string(cmd.Args[0]))
			}
		case "VCREATE":
			if len(cmd.Args) >= 1 {
				name := string(cmd.Args[0])
				// Parse args... (simplified logic)
				idx := &indexState{
					metric:    distance.Euclidean,
					precision: distance.Float32,
					entries:   make(map[string]vectorEntry),
				}

				for i := 1; i < len(cmd.Args)-1; i += 2 {
					key := strings.ToUpper(string(cmd.Args[i]))
					val := string(cmd.Args[i+1])
					switch key {
					case "METRIC":
						idx.metric = distance.DistanceMetric(val)
					case "M":
						idx.m, _ = strconv.Atoi(val)
					case "EF_CONSTRUCTION":
						idx.efConstruction, _ = strconv.Atoi(val)
					case "PRECISION":
						idx.precision = distance.PrecisionType(val)
					case "TEXT_LANGUAGE":
						idx.textLanguage = val
					}
				}
				if _, exists := indexes[name]; !exists {
					indexes[name] = idx
				}
			}
		case "VADD":
			if len(cmd.Args) >= 3 {
				idxName := string(cmd.Args[0])
				id := string(cmd.Args[1])
				vecStr := string(cmd.Args[2])

				if idx, ok := indexes[idxName]; ok {
					vec, err := parseVectorFromString(vecStr)
					if err == nil {
						entry := vectorEntry{vector: vec}
						if len(cmd.Args) > 3 && len(cmd.Args[3]) > 0 {
							var meta map[string]any
							if json.Unmarshal(cmd.Args[3], &meta) == nil {
								entry.metadata = meta
							}
						}
						idx.entries[id] = entry
					}
				}
			}
		case "VDEL":
			if len(cmd.Args) == 2 {
				idxName := string(cmd.Args[0])
				id := string(cmd.Args[1])
				if idx, ok := indexes[idxName]; ok {
					delete(idx.entries, id)
				}
			}
		}
	}

	// Phase 2: Apply to Core
	// KV
	for k, v := range kvData {
		e.DB.GetKVStore().Set(k, v)
	}

	// Indexes
	for name, state := range indexes {
		// Create Index if not exists
		if _, ok := e.DB.GetVectorIndex(name); !ok {
			e.DB.CreateVectorIndex(name, state.metric, state.m, state.efConstruction, state.precision, state.textLanguage)
		}

		idx, ok := e.DB.GetVectorIndex(name)
		if !ok {
			continue
		}

		// Bulk Load items
		for id, entry := range state.entries {
			// Internal Add
			internalID, err := idx.Add(id, entry.vector)
			if err == nil && len(entry.metadata) > 0 {
				// Clean internal flags if present
				if _, ok := entry.metadata["__deleted"]; ok {
					delete(entry.metadata, "__deleted")
				}
				e.DB.AddMetadataUnlocked(name, internalID, entry.metadata)
			}
		}
	}

	return nil
}

// SaveSnapshot creates a .kdb snapshot and truncates the AOF.
func (e *Engine) SaveSnapshot() error {
	e.adminMu.Lock()
	defer e.adminMu.Unlock()
	return e.saveSnapshotLocked()
}

// saveSnapshotLocked creates a .kdb snapshot and truncates the AOF.
func (e *Engine) saveSnapshotLocked() error {
	tempSnap := e.snapPath + ".tmp"
	f, err := os.Create(tempSnap)
	if err != nil {
		return err
	}

	if err := e.DB.Snapshot(f); err != nil {
		f.Close()
		return err
	}
	f.Close()

	if err := os.Rename(tempSnap, e.snapPath); err != nil {
		return err
	}

	if err := e.AOF.Truncate(); err != nil {
		return err
	}

	atomic.StoreInt64(&e.dirtyCounter, 0)
	e.lastSaveTime = time.Now()
	return nil
}

// RewriteAOF compacts the AOF file in background.
func (e *Engine) RewriteAOF() error {
	e.DB.Lock() // Global lock to ensure consistent state reading
	defer e.DB.Unlock()

	tempAof := filepath.Join(e.opts.DataDir, "rewrite.tmp")
	f, err := os.Create(tempAof)
	if err != nil {
		return err
	}
	defer os.Remove(tempAof)

	// 1. Write KV
	e.DB.IterateKVUnlocked(func(pair core.KVPair) {
		cmd := persistence.FormatCommand("SET", []byte(pair.Key), pair.Value)
		f.WriteString(cmd)
	})

	// 2. Write Indexes & Vectors
	infoList, _ := e.DB.GetVectorIndexInfoUnlocked()
	for _, info := range infoList {
		// VCREATE
		cmd := persistence.FormatCommand("VCREATE",
			[]byte(info.Name),
			[]byte("METRIC"), []byte(info.Metric),
			[]byte("PRECISION"), []byte(info.Precision),
			[]byte("M"), []byte(strconv.Itoa(info.M)),
			[]byte("EF_CONSTRUCTION"), []byte(strconv.Itoa(info.EfConstruction)),
			[]byte("TEXT_LANGUAGE"), []byte(info.TextLanguage),
		)
		f.WriteString(cmd)
	}

	e.DB.IterateVectorIndexesUnlocked(func(idxName string, _ *hnsw.Index, data core.VectorData) {
		vecStr := float32SliceToString(data.Vector)
		var meta []byte
		if len(data.Metadata) > 0 {
			meta, _ = json.Marshal(data.Metadata)
		}
		cmd := persistence.FormatCommand("VADD", []byte(idxName), []byte(data.ID), []byte(vecStr), meta)
		f.WriteString(cmd)
	})

	f.Close()

	// Atomic swap managed by AOFWriter
	if err := e.AOF.ReplaceWith(tempAof); err != nil {
		return err
	}

	info, _ := e.AOF.File().Stat()
	e.aofBaseSize = info.Size()
	return nil
}
