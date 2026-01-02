package engine

import (
	"encoding/json"
	"fmt"
	"slices"

	//"strings"

	"sync/atomic"

	"github.com/sanonone/kektordb/pkg/persistence"
)

// Graph Relationship Model
// Key format in KV Store: "rel:<source_id>:<relation_type>"
// Value: JSON list of target IDs ["t1", "t2"]

const relPrefix = "rel:"

func makeRelKey(sourceID, relType string) string {
	return fmt.Sprintf("%s%s:%s", relPrefix, sourceID, relType)
}

// VLink creates a directed edge between two nodes.
// If 'inverseRelationType' is provided (not empty), it also creates the reverse link.
func (e *Engine) VLink(sourceID, targetID, relationType, inverseRelationType string) error {
	e.adminMu.Lock()
	defer e.adminMu.Unlock()

	// 1. Direct Link (Source -> Target)
	if err := e.setLinkInternal(sourceID, targetID, relationType); err != nil {
		return err
	}

	// 2. Inverse Link (Target -> Source) - Optional
	if inverseRelationType != "" {
		if err := e.setLinkInternal(targetID, sourceID, inverseRelationType); err != nil {
			// Note: If this fails, we might have a partially inconsistent graph.
			// TODO: Implement rollback for production systems. For now, log/return error.
			return fmt.Errorf("failed to create inverse link: %w", err)
		}
	}

	atomic.AddInt64(&e.dirtyCounter, 1)
	return nil
}

// setLinkInternal is the raw write logic (without global lock, as the caller holds it)
func (e *Engine) setLinkInternal(src, dst, rel string) error {
	key := makeRelKey(src, rel)

	var targets []string
	val, found := e.DB.GetKVStore().Get(key)
	if found {
		_ = json.Unmarshal(val, &targets) // Ignore unmarshal errors on corrupt data
	}

	if slices.Contains(targets, dst) {
		return nil
	}
	targets = append(targets, dst)

	newVal, err := json.Marshal(targets)
	if err != nil {
		return err
	}

	// AOF & Memory
	cmd := persistence.FormatCommand("SET", []byte(key), newVal)
	if err := e.AOF.Write(cmd); err != nil {
		return err
	}
	e.DB.GetKVStore().Set(key, newVal)
	return nil
}

func (e *Engine) VUnlink(sourceID, targetID, relationType, inverseRelationType string) error {
	e.adminMu.Lock()
	defer e.adminMu.Unlock()

	// Direct Unlink
	if err := e.removeLinkInternal(sourceID, targetID, relationType); err != nil {
		return err
	}

	// Inverse Unlink
	if inverseRelationType != "" {
		if err := e.removeLinkInternal(targetID, sourceID, inverseRelationType); err != nil {
			return fmt.Errorf("failed to remove inverse link: %w", err)
		}
	}

	atomic.AddInt64(&e.dirtyCounter, 1)
	return nil
}

// removeLinkInternal (logic extracted from VUnlink)
func (e *Engine) removeLinkInternal(src, dst, rel string) error {
	key := makeRelKey(src, rel)
	val, found := e.DB.GetKVStore().Get(key)
	if !found {
		return nil
	}

	var targets []string
	if err := json.Unmarshal(val, &targets); err != nil {
		return err
	}

	newTargets := make([]string, 0, len(targets))
	changed := false
	for _, t := range targets {
		if t != dst {
			newTargets = append(newTargets, t)
		} else {
			changed = true
		}
	}

	if !changed {
		return nil
	}

	if len(newTargets) == 0 {
		cmd := persistence.FormatCommand("DEL", []byte(key))
		e.AOF.Write(cmd)
		e.DB.GetKVStore().Delete(key)
	} else {
		newVal, _ := json.Marshal(newTargets)
		cmd := persistence.FormatCommand("SET", []byte(key), newVal)
		e.AOF.Write(cmd)
		e.DB.GetKVStore().Set(key, newVal)
	}
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
	// Note: Standard KVStore does not support efficient prefix scan.
	// We cannot iterate brute-force on the KVStore as it would be too slow with millions of keys.
	//
	// SOLUTION V1: User must know 'relationType' to search.
	// If we want "give me everything", we should structure the key differently
	// or maintain an index of relation types per node.
	//
	// To keep it "Lightweight", we currently only implement specific GetLinks.
	return nil
}
