package engine

import (
	"encoding/json"
	"fmt"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/core/types"
	"github.com/sanonone/kektordb/pkg/persistence"
	"strconv"
	"sync/atomic"
)

// --- KV Operations ---

func (e *Engine) KVSet(key string, value []byte) error {
	// 1. AOF
	cmd := persistence.FormatCommand("SET", []byte(key), value)
	if err := e.AOF.Write(cmd); err != nil {
		return err
	}

	// 2. Memory
	e.DB.GetKVStore().Set(key, value)

	atomic.AddInt64(&e.dirtyCounter, 1)
	return nil
}

func (e *Engine) KVGet(key string) ([]byte, bool) {
	return e.DB.GetKVStore().Get(key)
}

func (e *Engine) KVDelete(key string) error {
	cmd := persistence.FormatCommand("DEL", []byte(key))
	if err := e.AOF.Write(cmd); err != nil {
		return err
	}
	e.DB.GetKVStore().Delete(key)
	atomic.AddInt64(&e.dirtyCounter, 1)
	return nil
}

// --- Vector Operations ---

func (e *Engine) VCreate(name string, metric distance.DistanceMetric, m, efC int, prec distance.PrecisionType, lang string) error {
	cmd := persistence.FormatCommand("VCREATE",
		[]byte(name),
		[]byte("METRIC"), []byte(metric),
		[]byte("M"), []byte(strconv.Itoa(m)),
		[]byte("EF_CONSTRUCTION"), []byte(strconv.Itoa(efC)),
		[]byte("PRECISION"), []byte(prec),
		[]byte("TEXT_LANGUAGE"), []byte(lang),
	)
	if err := e.AOF.Write(cmd); err != nil {
		return err
	}

	err := e.DB.CreateVectorIndex(name, metric, m, efC, prec, lang)
	if err == nil {
		atomic.AddInt64(&e.dirtyCounter, 1)
	}
	return err
}

func (e *Engine) VAdd(indexName, id string, vector []float32, metadata map[string]any) error {
	idx, ok := e.DB.GetVectorIndex(indexName)
	if !ok {
		return fmt.Errorf("index not found")
	}

	// 1. Memory Add
	internalID, err := idx.Add(id, vector)
	if err != nil {
		return err
	}

	// 2. Metadata
	if len(metadata) > 0 {
		e.DB.AddMetadataUnlocked(indexName, internalID, metadata)
	}

	// 3. Persistence
	vecStr := float32SliceToString(vector)
	var metaBytes []byte
	if len(metadata) > 0 {
		metaBytes, _ = json.Marshal(metadata)
	}

	cmd := persistence.FormatCommand("VADD", []byte(indexName), []byte(id), []byte(vecStr), metaBytes)
	if err := e.AOF.Write(cmd); err != nil {
		// Warn: Persistence failed but memory success. Inconsistency risk.
		return err
	}

	atomic.AddInt64(&e.dirtyCounter, 1)
	return nil
}

func (e *Engine) VDelete(indexName, id string) error {
	idx, ok := e.DB.GetVectorIndex(indexName)
	if !ok {
		return fmt.Errorf("index not found")
	}

	// Memory
	idx.Delete(id)

	// Disk
	cmd := persistence.FormatCommand("VDEL", []byte(indexName), []byte(id))
	if err := e.AOF.Write(cmd); err != nil {
		return err
	}

	atomic.AddInt64(&e.dirtyCounter, 1)
	return nil
}

// VSearch is read-only, so no AOF interaction.
func (e *Engine) VSearch(indexName string, query []float32, k int, filter string, efSearch int, alpha float64) ([]string, error) {
	// Logica copiata/adattata dall'handler search
	// Per ora ritorniamo solo gli ID stringa per semplicità,
	// ma in futuro potremmo ritornare struct complete

	idx, ok := e.DB.GetVectorIndex(indexName)
	if !ok {
		return nil, fmt.Errorf("index not found")
	}
	hnswIdx := idx.(*hnsw.Index)

	// ... (Logica ibrida semplificata per brevità) ...
	// Qui dovresti chiamare idx.SearchWithScores o l'hybrid search di DB
	// Per ora facciamo solo vector search diretta per far compilare

	results := idx.SearchWithScores(query, k, nil, efSearch)
	ids := make([]string, len(results))
	for i, r := range results {
		ids[i], _ = hnswIdx.GetExternalID(r.DocID)
	}
	return ids, nil
}

func (e *Engine) VAddBatch(indexName string, items []types.BatchObject) error {
	idx, ok := e.DB.GetVectorIndex(indexName)
	if !ok {
		return fmt.Errorf("index not found")
	}

	hnswIdx, ok := idx.(*hnsw.Index)
	if !ok {
		return fmt.Errorf("not hnsw")
	}

	// 1. Memory Batch
	if err := hnswIdx.AddBatch(items); err != nil {
		return err
	}

	// 2. Persistence Loop & Metadata
	for _, item := range items {
		if len(item.Metadata) > 0 {
			id := hnswIdx.GetInternalID(item.Id)
			e.DB.AddMetadataUnlocked(indexName, id, item.Metadata)
		}

		vecStr := float32SliceToString(item.Vector)
		var meta []byte
		if len(item.Metadata) > 0 {
			meta, _ = json.Marshal(item.Metadata)
		}

		cmd := persistence.FormatCommand("VADD", []byte(indexName), []byte(item.Id), []byte(vecStr), meta)
		// Nota: questo scrive N volte su disco. Non efficientissimo, ma sicuro.
		// Future optimization: Buffered AOF writer.
		e.AOF.Write(cmd)
	}

	atomic.AddInt64(&e.dirtyCounter, int64(len(items)))
	return nil
}
