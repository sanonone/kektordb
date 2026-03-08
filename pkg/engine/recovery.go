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
				idxName := string(cmd.Args[0])
				delete(indexes, idxName)

				// --- CLEANUP DEI GHOST FILES ---
				arenaPath := filepath.Join(e.opts.DataDir, "arenas", idxName)
				_ = os.RemoveAll(arenaPath)
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
		case "VMETA":
			// VMETA <IndexName> <ID> <MetadataJSON>
			if len(cmd.Args) == 3 {
				idxName := string(cmd.Args[0])
				id := string(cmd.Args[1])

				if idx, ok := indexes[idxName]; ok {
					var meta map[string]any
					if json.Unmarshal(cmd.Args[2], &meta) == nil {
						// Check if an entry already exists (created by a previous VADD)
						if entry, exists := idx.entries[id]; exists {
							if entry.metadata == nil {
								entry.metadata = make(map[string]any)
							}
							// Merge new metadata
							for k, v := range meta {
								entry.metadata[k] = v
							}
							idx.entries[id] = entry // Save the modified struct to the map
						} else {
							// Edge case: VMETA arrives without a VADD (e.g. corrupt AOF).
							// We create an entry without a vector to preserve the metadata.
							idx.entries[id] = vectorEntry{metadata: meta}
						}
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
		case "GLINK":
			if len(cmd.Args) >= 6 {
				sourceID := string(cmd.Args[0])
				targetID := string(cmd.Args[1])
				relType := string(cmd.Args[2])
				invRelType := string(cmd.Args[3])

				weight, _ := strconv.ParseFloat(string(cmd.Args[4]), 32)
				props := cmd.Args[5]

				var ts int64
				if len(cmd.Args) >= 7 {
					ts, _ = strconv.ParseInt(string(cmd.Args[6]), 10, 64)
				} else {
					ts = time.Now().UnixNano()
				}

				e.DB.AddEdge(sourceID, targetID, relType, float32(weight), props, ts)
				if invRelType != "" {
					e.DB.AddEdge(targetID, sourceID, invRelType, float32(weight), props, ts)
				}
			}
		case "GUNLINK":
			if len(cmd.Args) >= 5 {
				sourceID := string(cmd.Args[0])
				targetID := string(cmd.Args[1])
				relType := string(cmd.Args[2])
				invRelType := string(cmd.Args[3])
				hardDelete := string(cmd.Args[4]) == "true"

				var ts int64
				if len(cmd.Args) >= 6 {
					ts, _ = strconv.ParseInt(string(cmd.Args[5]), 10, 64)
				} else {
					ts = time.Now().UnixNano()
				}

				e.DB.RemoveEdge(sourceID, targetID, relType, hardDelete, ts)
				if invRelType != "" {
					e.DB.RemoveEdge(targetID, sourceID, invRelType, hardDelete, ts)
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
			arenaPath := filepath.Join(e.opts.DataDir, "arenas", name)
			e.DB.CreateVectorIndex(name, state.metric, state.m, state.efConstruction, state.precision, state.textLanguage, arenaPath)
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
				e.DB.AddMetadata(name, internalID, entry.metadata)
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
//
// FIX DEADLOCK (v2): Usa il pattern "Snapshot + Release" per evitare di tenere
// sia s.mu che h.metaMu contemporaneamente, eliminando un deadlock classico con AddBatch.
//
// Il meccanismo precedente teneva DB.RLock() per tutta la durata dell'operazione.
// Durante quella finestra, IterateVectorIndexesUnlocked → hnsw.Iterate() acquisiva
// h.metaMu.RLock(). Se AddBatch era in coda per h.metaMu.Lock(), il runtime Go
// bloccava qualsiasi nuovo reader su h.metaMu — incluso RewriteAOF stesso —
// causando un blocco permanente.
//
// Soluzione: acquisire s.mu.RLock brevemente solo per copiare i dati necessari,
// poi rilasciare il lock PRIMA di iterare su HNSW e scrivere su disco.
func (e *Engine) RewriteAOF() error {
	// If there is already a rewrite in progress, we exit immediately.
	// CompareAndSwap is a CPU-level atomic operation.
	if !e.isRewriting.CompareAndSwap(false, true) {
		// No critical errors, just say it's not necessary to do this now
		return nil
	}
	defer e.isRewriting.Store(false)

	tempAof := filepath.Join(e.opts.DataDir, "rewrite.tmp")

	writer, err := persistence.NewAOFWriter(tempAof)
	if err != nil {
		return err
	}
	defer func() {
		writer.Close()
		os.Remove(tempAof)
	}()

	// =========================================================================
	// STEP 1: Snapshot KV — lock breve su s.mu per copiare le coppie chiave/valore.
	// =========================================================================
	var kvPairs []core.KVPair
	e.DB.RLock()
	e.DB.IterateKVUnlocked(func(pair core.KVPair) {
		if strings.HasPrefix(pair.Key, "rel:") || strings.HasPrefix(pair.Key, "rev:") {
			return
		}
		// Copiamo il valore per evitare race su slice condivise.
		valCopy := make([]byte, len(pair.Value))
		copy(valCopy, pair.Value)
		kvPairs = append(kvPairs, core.KVPair{Key: pair.Key, Value: valCopy})
	})
	e.DB.RUnlock()

	// =========================================================================
	// STEP 2: Snapshot riferimenti agli indici — lock breve su s.mu.
	// Raccogliamo solo i *puntatori* (non i dati) e le info di configurazione.
	// =========================================================================
	type indexRef struct {
		info    core.IndexInfo
		hnswIdx *hnsw.Index
	}

	var indexRefs []indexRef
	e.DB.RLock()
	infoList, _ := e.DB.GetVectorIndexInfoUnlocked()
	for _, info := range infoList {
		idx, _ := e.DB.GetVectorIndexUnlocked(info.Name)
		if h, ok := idx.(*hnsw.Index); ok {
			indexRefs = append(indexRefs, indexRef{info: info, hnswIdx: h})
		}
	}
	e.DB.RUnlock()
	// s.mu è ora libero. Da qui in poi: nessun lock globale su DB.

	// =========================================================================
	// STEP 3: Per ogni indice, snapshot locale dei vettori.
	// Ogni chiamata a Iterate() acquisisce h.metaMu.RLock() in modo indipendente
	// (lock breve per ogni indice) senza tenere s.mu.
	// =========================================================================
	type vectorEntry struct {
		idxName  string
		id       string
		vector   []float32
		metadata map[string]any
	}
	type indexSnapshot struct {
		ref     indexRef
		rules   []hnsw.AutoLinkRule
		vectors []vectorEntry
	}

	snapshots := make([]indexSnapshot, 0, len(indexRefs))
	for _, ref := range indexRefs {
		snap := indexSnapshot{ref: ref}

		// GetAutoLinks() acquisisce h.metaMu.RLock internamente — OK perché non
		// teniamo s.mu in questo momento.
		snap.rules = ref.hnswIdx.GetAutoLinks()

		// Iterate() acquisisce h.metaMu.RLock internamente.
		// La callback viene eseguita mentre h.metaMu.RLock è tenuto, quindi
		// copiamo i dati prima di uscire dal lock.
		ref.hnswIdx.Iterate(func(id string, vector []float32) {
			vecCopy := make([]float32, len(vector))
			copy(vecCopy, vector)

			// GetInternalIDUnlocked: sicuro perché siamo dentro Iterate() che
			// tiene già h.metaMu.RLock.
			internalID, _ := ref.hnswIdx.GetInternalIDUnlocked(id)

			// GetMetadataForNodeUnlocked accede direttamente a metadataMap
			// senza acquisire s.mu (è la versione "Unlocked"). Non aggiungiamo
			// s.mu.RLock() qui per evitare il lock ordering:
			//   h.metaMu:R → s.mu:R
			// che potrebbe causare starvation se altri writer attendono s.mu:W.
			// Per una compaction AOF, la consistenza eventual è accettabile.
			meta := e.DB.GetMetadataForNodeUnlocked(ref.info.Name, internalID)

			snap.vectors = append(snap.vectors, vectorEntry{
				idxName:  ref.info.Name,
				id:       id,
				vector:   vecCopy,
				metadata: meta,
			})
		})

		snapshots = append(snapshots, snap)
	}

	// =========================================================================
	// STEP 4: Scrittura su disco — nessun lock globale tenuto.
	// =========================================================================

	// 4a. Scrittura KV
	for _, pair := range kvPairs {
		cmd := persistence.FormatCommand("SET", []byte(pair.Key), pair.Value)
		writer.Write(cmd)
	}

	// 4b. Scrittura Grafo in Memoria
	// Iteriamo in modo sicuro usando la helper che abbiamo aggiunto in core.go
	e.DB.IterateGraphEdges(func(source, target, rel string, weight float32, props []byte, cTime, dTime int64) {
		weightStr := strconv.FormatFloat(float64(weight), 'f', -1, 32)
		cTimeStr := strconv.FormatInt(cTime, 10)

		// 1. Scrive il comando di creazione
		cmdAdd := persistence.FormatCommand("GLINK", []byte(source), []byte(target), []byte(rel), []byte(""), []byte(weightStr), props, []byte(cTimeStr))
		writer.Write(cmdAdd)

		// 2. Se l'arco era "Soft Deleted" (Time Travel), scriviamo anche la sua cancellazione
		if dTime > 0 {
			dTimeStr := strconv.FormatInt(dTime, 10)
			cmdDel := persistence.FormatCommand("GUNLINK", []byte(source), []byte(target), []byte(rel), []byte(""), []byte("false"), []byte(dTimeStr))
			writer.Write(cmdDel)
		}
	})

	// 4b. Scrittura VCREATE + VADD per ogni indice
	for _, snap := range snapshots {
		info := snap.ref.info

		var rulesBytes []byte
		if len(snap.rules) > 0 {
			rulesBytes, _ = json.Marshal(snap.rules)
		}

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
		writer.Write(persistence.FormatCommand("VCREATE", args...))

		for _, ve := range snap.vectors {
			vecStr := float32SliceToString(ve.vector)
			var metaBytes []byte
			if len(ve.metadata) > 0 {
				var err error
				metaBytes, err = json.Marshal(ve.metadata)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to marshal metadata for %s during AOF rewrite: %v\n", ve.id, err)
					metaBytes = []byte("{}")
				}
			} else {
				metaBytes = []byte("{}")
			}
			cmd := persistence.FormatCommand("VADD", []byte(ve.idxName), []byte(ve.id), []byte(vecStr), metaBytes)
			writer.Write(cmd)
		}
	}

	if err := writer.Flush(); err != nil {
		return err
	}
	writer.Close()

	if err := e.AOF.ReplaceWith(tempAof); err != nil {
		return err
	}

	fileInfo, _ := e.AOF.File().Stat()
	e.aofBaseSize.Store(fileInfo.Size())
	return nil
}
