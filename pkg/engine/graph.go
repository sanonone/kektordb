package engine

import (
	"encoding/json"
	"fmt"
	"slices"
	//"strings"

	"github.com/sanonone/kektordb/pkg/persistence"
	"sync/atomic"
)

// Graph Relationship Model
// Key format in KV Store: "rel:<source_id>:<relation_type>"
// Value: JSON list of target IDs ["t1", "t2"]

const relPrefix = "rel:"

func makeRelKey(sourceID, relType string) string {
	return fmt.Sprintf("%s%s:%s", relPrefix, sourceID, relType)
}

// VLink creates a directed edge between two nodes/items.
// Example: VLink("chunk_1", "doc_A", "parent")
func (e *Engine) VLink(sourceID, targetID, relationType string) error {
	// Usiamo il lock amministrativo (o uno dedicato) per garantire atomicità Read-Modify-Write
	// su questa specifica chiave. Per semplicità usiamo adminMu o DB.Lock
	e.adminMu.Lock()
	defer e.adminMu.Unlock()

	key := makeRelKey(sourceID, relationType)

	// 1. Leggi esistente
	var targets []string
	val, found := e.DB.GetKVStore().Get(key)
	if found {
		if err := json.Unmarshal(val, &targets); err != nil {
			// Se il formato è corrotto, sovrascriviamo o ritorniamo errore?
			// Logghiamo e resettiamo per robustezza.
			targets = []string{}
		}
	}

	// 2. Aggiungi e Deduplica
	// Evitiamo duplicati: se il link esiste già, non facciamo nulla
	if slices.Contains(targets, targetID) {
		return nil // Già collegato
	}
	targets = append(targets, targetID)

	// 3. Serializza
	newVal, err := json.Marshal(targets)
	if err != nil {
		return fmt.Errorf("failed to marshal relationships: %w", err)
	}

	// 4. Persistenza (AOF)
	// Usiamo un comando custom VLINK per chiarezza nel log,
	// ma internamente mappa su un SET KV.
	// Per mantenere compatibilità col replay AOF esistente (che conosce solo SET),
	// salviamo come SET.
	// "SET rel:source:type JSON_DATA"
	cmd := persistence.FormatCommand("SET", []byte(key), newVal)
	if err := e.AOF.Write(cmd); err != nil {
		return err
	}

	// 5. Update Memoria
	e.DB.GetKVStore().Set(key, newVal)

	atomic.AddInt64(&e.dirtyCounter, 1)
	return nil
}

// VUnlink removes a directed edge.
func (e *Engine) VUnlink(sourceID, targetID, relationType string) error {
	e.adminMu.Lock()
	defer e.adminMu.Unlock()

	key := makeRelKey(sourceID, relationType)

	val, found := e.DB.GetKVStore().Get(key)
	if !found {
		return nil // Nulla da cancellare
	}

	var targets []string
	if err := json.Unmarshal(val, &targets); err != nil {
		return err
	}

	// Trova e rimuovi
	newTargets := make([]string, 0, len(targets))
	changed := false
	for _, t := range targets {
		if t != targetID {
			newTargets = append(newTargets, t)
		} else {
			changed = true
		}
	}

	if !changed {
		return nil
	}

	// Se la lista è vuota, cancelliamo la chiave
	if len(newTargets) == 0 {
		cmd := persistence.FormatCommand("DEL", []byte(key))
		e.AOF.Write(cmd)
		e.DB.GetKVStore().Delete(key)
	} else {
		// Altrimenti aggiorniamo
		newVal, _ := json.Marshal(newTargets)
		cmd := persistence.FormatCommand("SET", []byte(key), newVal)
		e.AOF.Write(cmd)
		e.DB.GetKVStore().Set(key, newVal)
	}

	atomic.AddInt64(&e.dirtyCounter, 1)
	return nil
}

// VGetLinks retrieves all targets for a given source and relation type.
// Returns a list of IDs.
func (e *Engine) VGetLinks(sourceID, relationType string) ([]string, bool) {
	key := makeRelKey(sourceID, relationType)
	val, found := e.DB.GetKVStore().Get(key)
	if !found {
		return nil, false
	}

	var targets []string
	if err := json.Unmarshal(val, &targets); err != nil {
		return nil, false
	}
	return targets, true
}

// VGetRelations retrieves ALL relationships for a node (scanning keys).
// Useful for debugging or full context retrieval.
// Returns map[relation_type] -> []target_ids
func (e *Engine) VGetRelations(sourceID string) map[string][]string {
	// Nota: KVStore standard non supporta prefix scan efficiente.
	// Per ora facciamo una iterazione brute-force sul KVStore?
	// NO, è troppo lento se il KV ha milioni di chiavi.
	//
	// SOLUZIONE V1: L'utente deve sapere il 'relationType' per cercare.
	// Se vogliamo "dammi tutto", dovremmo strutturare la chiave diversamente
	// o mantenere un indice dei tipi di relazione per nodo.
	//
	// Per mantenere "Lightweight", per ora implementiamo solo GetLinks specifici.
	return nil
}
