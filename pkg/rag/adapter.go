package rag

import (
	"github.com/sanonone/kektordb/pkg/core"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/types"
	"github.com/sanonone/kektordb/pkg/engine"
)

// KektorAdapter wraps the Engine to satisfy the RAG Store interface.
type KektorAdapter struct {
	Engine *engine.Engine
}

func NewKektorAdapter(eng *engine.Engine) *KektorAdapter {
	return &KektorAdapter{Engine: eng}
}

func (a *KektorAdapter) AddBatch(indexName string, items []types.BatchObject) error {
	// Use VAddBatch which handles persistence (AOF)
	return a.Engine.VAddBatch(indexName, items)
}

func (a *KektorAdapter) Delete(indexName string, id string) error {
	return a.Engine.VDelete(indexName, id)
}

func (a *KektorAdapter) SetState(key string, value []byte) error {
	return a.Engine.KVSet(key, value)
}

func (a *KektorAdapter) GetState(key string) ([]byte, bool) {
	return a.Engine.KVGet(key)
}

func (a *KektorAdapter) IndexExists(name string) bool {
	_, ok := a.Engine.DB.GetVectorIndex(name)
	return ok
}

func (a *KektorAdapter) CreateVectorIndex(name string, metric string, m int, efC int, precision string, lang string) error {
	var dMetric distance.DistanceMetric
	switch metric {
	case "euclidean":
		dMetric = distance.Euclidean
	default:
		dMetric = distance.Cosine
	}

	var dPrec distance.PrecisionType
	switch precision {
	case "float16":
		dPrec = distance.Float16
	case "int8":
		dPrec = distance.Int8
	default:
		dPrec = distance.Float32
	}

	// Call to Engine with dynamic parameters!
	// Note: passing nil for AutoMaintenanceConfig for now,
	// but you might want to configure that in YAML in the future.
	return a.Engine.VCreate(name, dMetric, m, efC, dPrec, lang, nil, nil)
}

func (a *KektorAdapter) Search(indexName string, query []float32, k int) ([]string, error) {
	// Call Engine's VSearch (use default for efSearch and alpha for now)
	return a.Engine.VSearch(indexName, query, k, "", "", 0, 0.5, nil)
}

func (a *KektorAdapter) GetMany(indexName string, ids []string) ([]core.VectorData, error) {
	return a.Engine.VGetMany(indexName, ids)
}

func (a *KektorAdapter) Link(sourceID, targetID, relationType, inverseRelationType string) error {
	return a.Engine.VLink(sourceID, targetID, relationType, inverseRelationType)
}
