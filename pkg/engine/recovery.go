package engine

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

// replayAOF reads the AOF file using the Framed Binary Protocol (RFC 003).
// It reconstructs the database state by replaying commands.
//
// CRASH RECOVERY:
// If the file is corrupted (e.g., due to power loss during write), this function
// will detect the corruption (checksum mismatch or incomplete frame), log a warning,
// and automatically TRUNCATE the file to the last valid offset. This ensures the
// database can always start, potentially losing only the last partially written instruction.
func (e *Engine) replayAOF() error {
	// We need Read/Write access to perform Truncate if necessary.
	file, err := os.OpenFile(e.aofPath, os.O_RDWR, 0666)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No AOF file implies a fresh start.
		}
		return err
	}
	defer file.Close()

	// Temporary state structures for compaction (Map-Reduce style loading)
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
		maintenanceCfg *hnsw.AutoMaintenanceConfig
		autoLinks      []hnsw.AutoLinkRule
		memoryConfig   *hnsw.MemoryConfig
	}

	kvData := make(map[string][]byte)
	indexes := make(map[string]*indexState)

	// validOffset tracks the end position of the last successfully verified frame.
	var validOffset int64 = 0
	corrupted := false

	// Loop through the file, frame by frame.
	for {
		// 1. Read binary frame (header + payload) with Checksum validation.
		payload, frameSize, err := persistence.ReadFrame(file)
		if err == io.EOF {
			break // Clean end of file.
		}

		if err != nil {
			// --- ERROR HANDLING & RECOVERY STRATEGY ---

			// Case A: Legacy File Detection
			if errors.Is(err, persistence.ErrInvalidMagic) && validOffset == 0 {
				slog.Error("CRITICAL: Detected legacy/invalid AOF format. Auto-migration not supported in this version.", "path", e.aofPath)
				return fmt.Errorf("AOF format mismatch: file does not start with magic byte 0xA5")
			}

			// Case B: Corruption (Incomplete write or Bit rot)
			slog.Warn("AOF Corruption Detected", "error", err, "offset", validOffset)
			corrupted = true
			break // Stop reading, we will truncate later.
		}

		// 2. Parse the payload (The actual RESP command)
		// We wrap the payload byte slice in a reader to reuse the existing RESP parser.
		cmdReader := bufio.NewReader(bytes.NewReader(payload))
		cmd, err := persistence.ParseCommand(cmdReader)
		if err != nil {
			slog.Warn("AOF contains valid frame but invalid RESP command", "offset", validOffset)
			corrupted = true
			break
		}

		// 3. Aggregate Command (Compaction Logic)
		// This logic buffers changes in memory maps before applying them to the core DB.
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
						if m, err := strconv.Atoi(val); err == nil {
							idx.m = m
						} else {
							idx.m = 16
						}
					case "EF_CONSTRUCTION":
						if efC, err := strconv.Atoi(val); err == nil {
							idx.efConstruction = efC
						} else {
							idx.efConstruction = 200
						}
					case "PRECISION":
						idx.precision = distance.PrecisionType(val)
					case "TEXT_LANGUAGE":
						idx.textLanguage = val
					case "AUTO_LINKS":
						var rules []hnsw.AutoLinkRule
						// val is the JSON string
						if json.Unmarshal([]byte(val), &rules) == nil {
							idx.autoLinks = rules
						}
					case "MEMORY_CONFIG": // NEW
						var memCfg hnsw.MemoryConfig
						if json.Unmarshal([]byte(val), &memCfg) == nil {
							idx.memoryConfig = &memCfg
						}
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
		case "VCONFIG":
			if len(cmd.Args) == 2 {
				idxName := string(cmd.Args[0])
				cfgJSON := cmd.Args[1]
				if idxState, ok := indexes[idxName]; ok {
					var cfg hnsw.AutoMaintenanceConfig
					if json.Unmarshal(cfgJSON, &cfg) == nil {
						idxState.maintenanceCfg = &cfg
					}
				}
			}
		}

		// Advance the valid offset pointer.
		validOffset += int64(frameSize)
	}

	// 4. Auto-Truncate if corruption was found
	if corrupted {
		slog.Warn("Repairing AOF file...", "original_size", "unknown", "truncated_to", validOffset)
		if err := file.Truncate(validOffset); err != nil {
			return fmt.Errorf("failed to truncate corrupted AOF: %w", err)
		}
		// Sync ensures the OS writes the truncation to disk.
		if err := file.Sync(); err != nil {
			return fmt.Errorf("failed to sync repaired AOF: %w", err)
		}
		slog.Info("AOF repair successful. Database will start with recovered data.")
	}

	// 5. Apply Reconstructed State to Core DB
	// KV Store
	for k, v := range kvData {
		e.DB.GetKVStore().Set(k, v)
	}

	// Indexes
	for name, state := range indexes {
		// Create Index if it doesn't exist
		if _, ok := e.DB.GetVectorIndex(name); !ok {
			e.DB.CreateVectorIndex(name, state.metric, state.m, state.efConstruction, state.precision, state.textLanguage)
		}

		idx, ok := e.DB.GetVectorIndex(name)
		if !ok {
			continue
		}

		/// Cast to HNSW
		hnswIdx, isHnsw := idx.(*hnsw.Index)

		// Apply maintenance config
		if state.maintenanceCfg != nil && isHnsw {
			hnswIdx.UpdateMaintenanceConfig(*state.maintenanceCfg)
		}

		// Apply AutoLinks
		if len(state.autoLinks) > 0 && isHnsw {
			hnswIdx.SetAutoLinks(state.autoLinks)
		}

		// Apply memory config
		if state.memoryConfig != nil && isHnsw {
			hnswIdx.SetMemoryConfig(*state.memoryConfig)
		}

		// Bulk Load vectors
		for id, entry := range state.entries {
			internalID, err := idx.Add(id, entry.vector)
			if err == nil && len(entry.metadata) > 0 {
				delete(entry.metadata, "__deleted")
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

// saveSnapshotLocked performs the actual snapshotting logic.
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

// RewriteAOF compacts the AOF file in the background.
// Updated to use the new binary framing format.
func (e *Engine) RewriteAOF() error {
	e.DB.Lock() // Global lock to ensure consistent state
	defer e.DB.Unlock()

	tempAof := filepath.Join(e.opts.DataDir, "rewrite.tmp")

	// Open using standard file, but wrap in AOFWriter to get framing logic
	writer, err := persistence.NewAOFWriter(tempAof)
	if err != nil {
		return err
	}
	defer func() {
		writer.Close()
		os.Remove(tempAof) // Cleanup if not replaced
	}()

	// 1. Write KV
	e.DB.IterateKVUnlocked(func(pair core.KVPair) {
		cmd := persistence.FormatCommand("SET", []byte(pair.Key), pair.Value)
		// AOFWriter.Write handles framing automatically
		writer.Write(cmd)
	})

	// 2. Write Indexes & Vectors
	infoList, _ := e.DB.GetVectorIndexInfoUnlocked()
	for _, info := range infoList {
		// Retrieve the actual index object to get the rules
		// We need to lock the DB briefly or access unlocked if we are sure (RewriteAOF holds Lock)
		idx, _ := e.DB.GetVectorIndexUnlocked(info.Name)
		var rulesBytes []byte

		if hnswIdx, ok := idx.(*hnsw.Index); ok {
			rules := hnswIdx.GetAutoLinks()
			if len(rules) > 0 {
				rulesBytes, _ = json.Marshal(rules)
			}
		}

		// Build arguments dynamically
		args := [][]byte{
			[]byte(info.Name),
			[]byte("METRIC"), []byte(info.Metric),
			[]byte("PRECISION"), []byte(info.Precision),
			[]byte("M"), []byte(strconv.Itoa(info.M)),
			[]byte("EF_CONSTRUCTION"), []byte(strconv.Itoa(info.EfConstruction)),
			[]byte("TEXT_LANGUAGE"), []byte(info.TextLanguage),
		}

		if len(rulesBytes) > 0 {
			args = append(args, []byte("AUTO_LINKS"), rulesBytes)
		}

		cmd := persistence.FormatCommand("VCREATE", args...)
		writer.Write(cmd) // Note: using 'writer' from the previous refactoring
	}

	e.DB.IterateVectorIndexesUnlocked(func(idxName string, _ *hnsw.Index, data core.VectorData) {
		vecStr := float32SliceToString(data.Vector)
		var meta []byte
		if len(data.Metadata) > 0 {
			var err error
			meta, err = json.Marshal(data.Metadata)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to marshal metadata for %s during AOF rewrite: %v\n", data.ID, err)
				meta = nil
			}
		}
		cmd := persistence.FormatCommand("VADD", []byte(idxName), []byte(data.ID), []byte(vecStr), meta)
		writer.Write(cmd)
	})

	// Force flush before swapping
	if err := writer.Flush(); err != nil {
		return err
	}
	// Close explicitly to release handle
	writer.Close()

	// Atomic swap managed by the MAIN AOFWriter
	if err := e.AOF.ReplaceWith(tempAof); err != nil {
		return err
	}

	info, _ := e.AOF.File().Stat()
	e.aofBaseSize = info.Size()
	return nil
}
