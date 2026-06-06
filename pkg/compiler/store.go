package compiler

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"
)

const defaultArtifactVectorDim = 384

// StoreArtifact saves an artifact as a pinned graph node.
func (c *Compiler) StoreArtifact(artifact *Artifact, sources []NodeInfo, indexName string, policy RefreshPolicy) error {
	_ = policy

	existingID, existingVersion, err := c.findLatestArtifact(
		artifact.Name, artifact.EntityType, artifact.EntityID, indexName,
	)
	if err != nil {
		return fmt.Errorf("find existing artifact: %w", err)
	}

	if existingVersion > 0 {
		artifact.Version = existingVersion + 1
	} else {
		artifact.Version = 1
	}

	metadata := c.buildArtifactMetadata(artifact)

	vector := c.computeArtifactVector(sources, indexName)
	nodeID := artifactNodeID(artifact)

	if existingID != "" && policy.KeepHistory {
		// Mark previous version as historical
		if err := c.eng.VSetMetadata(indexName, existingID, map[string]any{
			"_is_historical": true,
			"_pinned":        false,
		}); err != nil {
			return fmt.Errorf("mark old artifact historical: %w", err)
		}

		// Link old → new
		if err := c.eng.VLink(indexName, existingID, nodeID,
			"superseded_by", "supersedes", 1.0, nil); err != nil {
			return fmt.Errorf("link old→new artifact: %w", err)
		}
	} else if existingID != "" {
		// KeepHistory is false: delete the old node entirely
		c.eng.VDelete(indexName, existingID)
	}

	if err := c.eng.VAdd(indexName, nodeID, vector, metadata); err != nil {
		return fmt.Errorf("create artifact node: %w", err)
	}

	for _, src := range sources {
		_ = c.eng.VLink(indexName, nodeID, src.ID, "compiled_from", "source_of", 1.0, nil)
	}

	artifact.ID = nodeID

	// Prune historical versions if configured
	c.pruneHistoricalVersions(artifact.Name, artifact.EntityType, artifact.EntityID, indexName, &policy)

	return nil
}

// buildArtifactMetadata serializes an artifact into graph node metadata.
func (c *Compiler) buildArtifactMetadata(artifact *Artifact) map[string]any {
	dataJSON, _ := json.Marshal(artifact.Data)
	provenanceJSON, _ := json.Marshal(artifact.Provenance)
	confidenceJSON, _ := json.Marshal(artifact.Confidence)

	metadata := map[string]any{
		"type":            "knowledge_artifact",
		"artifact_name":   artifact.Name,
		"version":         float64(artifact.Version),
		"entity_type":     artifact.EntityType,
		"entity_id":       artifact.EntityID,
		"data":            string(dataJSON),
		"provenance":      string(provenanceJSON),
		"confidence":      string(confidenceJSON),
		"compile_mode":    string(artifact.CompileMode),
		"staleness_score": artifact.StalenessScore,
		"status":          string(artifact.Status),
		"_pinned":         true,
		"_created_at":     float64(time.Now().UnixNano()) / 1e9,
	}

	if artifact.Schema != nil {
		schemaJSON, _ := json.Marshal(artifact.Schema)
		metadata["schema"] = string(schemaJSON)
	}
	if artifact.TaskSpec != nil {
		taskSpecJSON, _ := json.Marshal(artifact.TaskSpec)
		metadata["task_spec"] = string(taskSpecJSON)
	}

	return metadata
}

// GetArtifact retrieves a specific version (or the latest if version=0).
func (c *Compiler) GetArtifact(name, entityType, entityID, indexName string, version int) (*Artifact, error) {
	all, err := c.getAllArtifactVersions(name, entityType, entityID, indexName)
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("artifact not found: %s/%s/%s", name, entityType, entityID)
	}

	if version > 0 {
		for _, a := range all {
			if a.Version == version {
				return a, nil
			}
		}
		return nil, fmt.Errorf("version %d not found for artifact %s", version, name)
	}

	// Return latest (non-historical first, then highest version)
	for _, a := range all {
		if a.Status != CompileStatusStale {
			return a, nil
		}
	}
	return all[0], nil
}

// GetArtifactHistory returns all versions of an artifact, ordered by version desc.
func (c *Compiler) GetArtifactHistory(name, entityType, entityID, indexName string) ([]*Artifact, error) {
	return c.getAllArtifactVersions(name, entityType, entityID, indexName)
}

// ListArtifacts returns all non-historical artifacts in the given index.
func (c *Compiler) ListArtifacts(indexName string) ([]*Artifact, error) {
	ids, err := c.eng.VFilter(indexName, "type='knowledge_artifact'", 200)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}

	var artifacts []*Artifact
	for _, id := range ids {
		data, err := c.eng.VGet(indexName, id)
		if err != nil {
			continue
		}
		if hist, ok := data.Metadata["_is_historical"].(bool); ok && hist {
			continue
		}
		artifacts = append(artifacts, ArtifactFromMetadata(id, data.Metadata))
	}
	return artifacts, nil
}

// findLatestArtifact returns the ID and version of the latest (non-historical) artifact.
func (c *Compiler) findLatestArtifact(name, entityType, entityID, indexName string) (string, int, error) {
	all, err := c.getAllArtifactVersions(name, entityType, entityID, indexName)
	if err != nil {
		return "", 0, err
	}

	for _, a := range all {
		if a.Status != CompileStatusStale {
			return a.ID, a.Version, nil
		}
	}
	return "", 0, nil
}

// getAllArtifactVersions returns all versions of an artifact, sorted by version desc.
func (c *Compiler) getAllArtifactVersions(name, entityType, entityID, indexName string) ([]*Artifact, error) {
	filter := fmt.Sprintf(
		"type='knowledge_artifact' AND artifact_name='%s' AND entity_type='%s' AND entity_id='%s'",
		name, entityType, entityID,
	)
	ids, err := c.eng.VFilter(indexName, filter, 100)
	if err != nil {
		return nil, fmt.Errorf("filter artifacts: %w", err)
	}

	artifacts := make([]*Artifact, 0, len(ids))
	for _, id := range ids {
		data, err := c.eng.VGet(indexName, id)
		if err != nil {
			continue
		}
		artifacts = append(artifacts, ArtifactFromMetadata(id, data.Metadata))
	}

	sort.Slice(artifacts, func(i, j int) bool {
		return artifacts[i].Version > artifacts[j].Version
	})
	return artifacts, nil
}

// pruneHistoricalVersions removes old artifact versions according to policy.
func (c *Compiler) pruneHistoricalVersions(name, entityType, entityID, indexName string, policy *RefreshPolicy) {
	if !policy.KeepHistory {
		return
	}

	all, err := c.getAllArtifactVersions(name, entityType, entityID, indexName)
	if err != nil || len(all) < 2 {
		return
	}

	// Separate current from historical
	var historical []*Artifact
	for _, a := range all {
		if a.Status == CompileStatusStale {
			historical = append(historical, a)
		}
	}
	if len(historical) == 0 {
		return
	}

	// Sort oldest first for pruning
	sort.Slice(historical, func(i, j int) bool {
		return historical[i].CompiledAt.Before(historical[j].CompiledAt)
	})

	toDelete := make(map[string]bool)

	// Prune by max count
	if policy.MaxVersions > 0 {
		// Total versions = historical + 1 current
		keepHistorical := policy.MaxVersions - 1
		if keepHistorical < 0 {
			keepHistorical = 0
		}
		for i := 0; i < len(historical)-keepHistorical; i++ {
			toDelete[historical[i].ID] = true
		}
	}

	// Prune by age
	if policy.PruneAfterDays > 0 {
		cutoff := time.Now().Add(-time.Duration(policy.PruneAfterDays) * 24 * time.Hour)
		for _, h := range historical {
			if h.CompiledAt.Before(cutoff) && !h.CompiledAt.IsZero() {
				toDelete[h.ID] = true
			}
		}
	}

	for id := range toDelete {
		if err := c.eng.VDelete(indexName, id); err != nil {
			// Log but don't fail the whole compile
		}
	}
}

// computeArtifactVector returns the mean vector of source nodes,
// or a zero vector if embedder is unavailable.
func (c *Compiler) computeArtifactVector(sources []NodeInfo, indexName string) []float32 {
	if c.embedder == nil || len(sources) == 0 {
		return make([]float32, defaultArtifactVectorDim)
	}

	var accum []float32
	count := 0
	for _, src := range sources {
		data, err := c.eng.VGet(indexName, src.ID)
		if err != nil || len(data.Vector) == 0 {
			continue
		}
		if accum == nil {
			accum = make([]float32, len(data.Vector))
		}
		for i, v := range data.Vector {
			if i < len(accum) {
				accum[i] += v
			}
		}
		count++
	}

	if count == 0 || accum == nil {
		return make([]float32, defaultArtifactVectorDim)
	}

	for i := range accum {
		accum[i] /= float32(count)
	}
	return accum
}

// artifactNodeID generates a predictable node ID for an artifact version.
func artifactNodeID(artifact *Artifact) string {
	return fmt.Sprintf("_artifact_%s_%s_%s_v%s",
		artifact.Name, artifact.EntityType, artifact.EntityID,
		strconv.Itoa(artifact.Version))
}

// ArtifactFromMetadata reconstructs an Artifact from graph node metadata.
func ArtifactFromMetadata(id string, md map[string]any) *Artifact {
	a := &Artifact{
		ID:              id,
		Data:            make(map[string]any),
		Provenance:      make(map[string][]Provenance),
		Confidence:      make(map[string]float64),
		StalenessScore:  0,
		Status:          CompileStatusComplete,
		CompileMode:     CompileModeDeterministic,
		Version:         1,
	}

	if v, ok := md["artifact_name"].(string); ok {
		a.Name = v
	}
	if v, ok := md["version"].(float64); ok {
		a.Version = int(v)
	}
	if v, ok := md["entity_type"].(string); ok {
		a.EntityType = v
	}
	if v, ok := md["entity_id"].(string); ok {
		a.EntityID = v
	}
	if v, ok := md["compile_mode"].(string); ok {
		a.CompileMode = CompileMode(v)
	}
	if v, ok := md["status"].(string); ok {
		a.Status = CompileStatus(v)
	}
	if v, ok := md["staleness_score"].(float64); ok {
		a.StalenessScore = v
	}

	// Check _is_historical to set Stale status
	if hist, ok := md["_is_historical"].(bool); ok && hist {
		a.Status = CompileStatusStale
	}
	// Check _created_at
	if ca, ok := md["_created_at"].(float64); ok {
		a.CompiledAt = time.Unix(int64(ca), int64((ca-float64(int64(ca)))*1e9))
	}
	// Also check createdAt key
	if ca, ok := md["created_at"].(float64); ok {
		a.CompiledAt = time.Unix(int64(ca), 0)
	}

	if dataStr, ok := md["data"].(string); ok && dataStr != "" {
		var data map[string]any
		if json.Unmarshal([]byte(dataStr), &data) == nil {
			a.Data = data
		}
	}
	if provStr, ok := md["provenance"].(string); ok && provStr != "" {
		var prov map[string][]Provenance
		if json.Unmarshal([]byte(provStr), &prov) == nil {
			a.Provenance = prov
		}
	}
	if confStr, ok := md["confidence"].(string); ok && confStr != "" {
		var conf map[string]float64
		if json.Unmarshal([]byte(confStr), &conf) == nil {
			a.Confidence = conf
		}
	}
	if schemaStr, ok := md["schema"].(string); ok && schemaStr != "" {
		var schema OutputSchema
		if json.Unmarshal([]byte(schemaStr), &schema) == nil {
			a.Schema = &schema
		}
	}
	if taskStr, ok := md["task_spec"].(string); ok && taskStr != "" {
		var taskSpec TaskSpec
		if json.Unmarshal([]byte(taskStr), &taskSpec) == nil {
			a.TaskSpec = &taskSpec
		}
	}

	return a
}
