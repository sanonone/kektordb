package types

import "github.com/sanonone/kektordb/pkg/core/distance" // Importa distance

// SearchResult rappresenta un singolo risultato da una query, con il suo punteggio.
type SearchResult struct {
	DocID uint32
	Score float64
}

// Candidate è la struct interna di HNSW per i risultati, con ID interno e punteggio.
type Candidate struct {
	Id       uint32
	Distance float64
}

// NodeData è una struct per trasportare i dati di un nodo fuori dal package HNSW.
type NodeData struct {
	ID         string
	InternalID uint32
	Vector     []float32
	Metadata   map[string]interface{}
}

// IndexInfo modella le informazioni di un indice per l'API.
type IndexInfo struct {
	Name           string                  `json:"name"`
	Metric         distance.DistanceMetric `json:"metric"`
	Precision      distance.PrecisionType  `json:"precision"`
	M              int                     `json:"m"`
	EfConstruction int                     `json:"ef_construction"`
	VectorCount    int                     `json:"vector_count"`
	TextLanguage   string                  `json:"text_language"`
}
