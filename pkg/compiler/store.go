package compiler

import (
	"encoding/json"
	"fmt"
)

const defaultArtifactVectorDim = 384

// StoreArtifact saves an artifact as a pinned graph node with
// compiled_from relations to its source nodes.
func (c *Compiler) StoreArtifact(artifact *Artifact, sources []NodeInfo, indexName string) error {
	existingID, existingVersion, err := c.findExistingArtifact(
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
	}

	if artifact.Schema != nil {
		schemaJSON, _ := json.Marshal(artifact.Schema)
		metadata["schema"] = string(schemaJSON)
	}
	if artifact.TaskSpec != nil {
		taskSpecJSON, _ := json.Marshal(artifact.TaskSpec)
		metadata["task_spec"] = string(taskSpecJSON)
	}

	vector := computeArtifactVector(sources)
	nodeID := artifactNodeID(artifact.Name, artifact.EntityType, artifact.EntityID)

	if existingID != "" {
		if err := c.eng.VSetMetadata(indexName, existingID, metadata); err != nil {
			return fmt.Errorf("update artifact metadata: %w", err)
		}
		nodeID = existingID
	} else {
		if err := c.eng.VAdd(indexName, nodeID, vector, metadata); err != nil {
			return fmt.Errorf("create artifact node: %w", err)
		}
	}

	for _, src := range sources {
		_ = c.eng.VLink(indexName, nodeID, src.ID, "compiled_from", "source_of", 1.0, nil)
	}

	artifact.ID = nodeID
	return nil
}

// GetArtifact retrieves the latest artifact by name and entity.
func (c *Compiler) GetArtifact(name, entityType, entityID, indexName string) (*Artifact, error) {
	filter := fmt.Sprintf(
		"type='knowledge_artifact' AND artifact_name='%s' AND entity_type='%s' AND entity_id='%s'",
		name, entityType, entityID,
	)
	ids, err := c.eng.VFilter(indexName, filter, 1)
	if err != nil {
		return nil, fmt.Errorf("filter artifacts: %w", err)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("artifact not found: %s/%s/%s", name, entityType, entityID)
	}

	data, err := c.eng.VGet(indexName, ids[0])
	if err != nil {
		return nil, fmt.Errorf("get artifact data: %w", err)
	}

	return ArtifactFromMetadata(ids[0], data.Metadata), nil
}

// ListArtifacts returns all artifacts in the given index.
func (c *Compiler) ListArtifacts(indexName string) ([]*Artifact, error) {
	ids, err := c.eng.VFilter(indexName, "type='knowledge_artifact'", 100)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}

	artifacts := make([]*Artifact, 0, len(ids))
	for _, id := range ids {
		data, err := c.eng.VGet(indexName, id)
		if err != nil {
			continue
		}
		artifacts = append(artifacts, ArtifactFromMetadata(id, data.Metadata))
	}
	return artifacts, nil
}

// findExistingArtifact returns the ID and version of an existing artifact.
func (c *Compiler) findExistingArtifact(name, entityType, entityID, indexName string) (string, int, error) {
	filter := fmt.Sprintf(
		"type='knowledge_artifact' AND artifact_name='%s' AND entity_type='%s' AND entity_id='%s'",
		name, entityType, entityID,
	)
	ids, err := c.eng.VFilter(indexName, filter, 1)
	if err != nil || len(ids) == 0 {
		return "", 0, nil
	}

	data, err := c.eng.VGet(indexName, ids[0])
	if err != nil {
		return "", 0, nil
	}

	version := 0
	if v, ok := data.Metadata["version"].(float64); ok {
		version = int(v)
	}
	return ids[0], version, nil
}

// computeArtifactVector returns a zero vector for the artifact dimension.
func computeArtifactVector(sources []NodeInfo) []float32 {
	return make([]float32, defaultArtifactVectorDim)
}

// artifactNodeID generates a predictable node ID for an artifact.
func artifactNodeID(name, entityType, entityID string) string {
	return fmt.Sprintf("_artifact_%s_%s_%s", name, entityType, entityID)
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
