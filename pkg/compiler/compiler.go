package compiler

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/sanonone/kektordb/pkg/embeddings"
	"github.com/sanonone/kektordb/pkg/engine"
	"github.com/sanonone/kektordb/pkg/llm"
)

// Compiler orchestrates the knowledge artifact compilation pipeline:
//  1. Query the graph for source nodes
//  2. Filter by relevance
//  3. Compile each field (deterministic or LLM-assisted)
//  4. Store the artifact as a pinned graph node
type Compiler struct {
	eng       *engine.Engine
	llm       llm.Client
	embedder  embeddings.Embedder
	templates map[string]CompileTemplate

	muPerArtifact sync.Map
	taskManager   *compileTaskManager
}

// NewCompiler creates a new Compiler backed by the given engine.
// If llmClient is nil, only deterministic compilation is available.
// If embedder is nil, semantic search and artifact vector averaging are unavailable.
func NewCompiler(eng *engine.Engine, llmClient llm.Client, emb embeddings.Embedder) *Compiler {
	return &Compiler{
		eng:         eng,
		llm:         llmClient,
		embedder:    emb,
		templates:   BuiltinTemplates,
		taskManager: newCompileTaskManager(),
	}
}

// resolveTemplate returns the template for the request, or nil if none matches.
func (c *Compiler) resolveTemplate(req CompileRequest) *CompileTemplate {
	if req.Template != "" {
		tmpl, err := GetTemplate(req.Template)
		if err == nil {
			return tmpl
		}
	}
	if req.TaskSpec != nil {
		return nil // custom task spec, no template
	}
	// Try to match by name
	tmpl, err := GetTemplate(req.Name)
	if err == nil {
		return tmpl
	}
	return nil
}

// resolveMode determines the compilation mode from the request and template.
func (c *Compiler) resolveMode(req CompileRequest, template *CompileTemplate) CompileMode {
	if req.CompileMode != "" && req.CompileMode != CompileModeAuto {
		return req.CompileMode
	}
	if template != nil {
		return template.CompileMode
	}
	// Auto: deterministic if no LLM available, otherwise hybrid
	if c.llm == nil {
		return CompileModeDeterministic
	}
	return CompileModeHybrid
}

// resolveSchema returns the output schema from the request or template.
func (c *Compiler) resolveSchema(req CompileRequest, template *CompileTemplate) OutputSchema {
	if req.TaskSpec != nil {
		return req.TaskSpec.OutputSchema
	}
	if template != nil {
		return template.Schema
	}
	return OutputSchema{}
}

// resolveConfidenceMin returns the minimum confidence threshold.
func (c *Compiler) resolveConfidenceMin(req CompileRequest, template *CompileTemplate) float64 {
	if req.TaskSpec != nil && req.TaskSpec.ConfidenceMin > 0 {
		return req.TaskSpec.ConfidenceMin
	}
	return 0.0
}

// resolveRefreshPolicy returns the refresh policy from the request or template.
func (c *Compiler) resolveRefreshPolicy(req CompileRequest, template *CompileTemplate) RefreshPolicy {
	if req.TaskSpec != nil && req.TaskSpec.RefreshPolicy.KeepHistory {
		return req.TaskSpec.RefreshPolicy
	}
	if template != nil {
		return template.RefreshPolicy
	}
	return RefreshPolicy{
		KeepHistory:    true,
		MaxVersions:    0,
		PruneAfterDays: 90,
	}
}

func (c *Compiler) artifactKey(indexName, name, entityType, entityID string) string {
	return fmt.Sprintf("%s:%s:%s:%s", indexName, name, entityType, entityID)
}

// Compile orchestrates the full compilation pipeline and returns
// a knowledge artifact. It uses per-artifact locking so compilations
// for different artifacts proceed in parallel.
func (c *Compiler) Compile(req CompileRequest) (*Artifact, error) {
	if req.IndexName == "" {
		req.IndexName = "mcp_memory"
	}

	key := c.artifactKey(req.IndexName, req.Name, req.Sources.Entity.Type, req.Sources.Entity.ID)
	muAny, _ := c.muPerArtifact.LoadOrStore(key, &sync.Mutex{})
	mu := muAny.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	template := c.resolveTemplate(req)
	mode := c.resolveMode(req, template)

	sourceNodes, err := c.QuerySources(req.Sources, req.IndexName)
	if err != nil {
		return nil, fmt.Errorf("query sources: %w", err)
	}
	if len(sourceNodes) == 0 {
		return nil, fmt.Errorf("no source nodes found for entity %s:%s",
			req.Sources.Entity.Type, req.Sources.Entity.ID)
	}

	relevantNodes := c.FilterByRelevance(sourceNodes, template, req.TaskSpec)

	artifact := &Artifact{
		Name:          req.Name,
		Version:       1,
		EntityType:    req.Sources.Entity.Type,
		EntityID:      req.Sources.Entity.ID,
		Data:          make(map[string]any),
		Provenance:    make(map[string][]Provenance),
		Confidence:    make(map[string]float64),
		SourceNodeIDs: nodeIDs(relevantNodes),
		CompileMode:   mode,
		Status:        CompileStatusCompiling,
		CompiledAt:    time.Now(),
	}

	if template != nil {
		artifact.Schema = &template.Schema
	}
	if req.TaskSpec != nil {
		artifact.TaskSpec = req.TaskSpec
	}

	schema := c.resolveSchema(req, template)
	confidenceMin := c.resolveConfidenceMin(req, template)

	for fieldName, fieldDef := range schema.Properties {
		compiled, provenance, confidence, err := c.compileField(
			fieldName, fieldDef, relevantNodes, req, template, mode,
		)
		if err != nil {
			slog.Warn("compile field failed", "field", fieldName, "err", err)
			continue
		}
		if compiled == nil {
			continue
		}
		if confidence < confidenceMin {
			continue
		}
		artifact.Data[fieldName] = compiled
		if len(provenance) > 0 {
			artifact.Provenance[fieldName] = provenance
		}
		artifact.Confidence[fieldName] = confidence
	}

	// Resolve refresh policy: request TaskSpec > template > defaults
	policy := c.resolveRefreshPolicy(req, template)

	if err := c.StoreArtifact(artifact, relevantNodes, req.IndexName, policy); err != nil {
		artifact.Status = CompileStatusFailed
		return artifact, fmt.Errorf("store artifact: %w", err)
	}

	artifact.Status = CompileStatusComplete
	return artifact, nil
}

// compileField compiles a single field, routing to deterministic
// or LLM-assisted compilation based on the field definition and mode.
func (c *Compiler) compileField(
	fieldName string,
	fieldDef FieldDef,
	nodes []NodeInfo,
	req CompileRequest,
	template *CompileTemplate,
	mode CompileMode,
) (value any, provenance []Provenance, confidence float64, err error) {
	if c.needsLLMForField(fieldDef, mode) {
		return c.compileFieldLLM(fieldName, fieldDef, nodes, req, template)
	}
	return c.compileFieldDeterministic(fieldName, fieldDef, nodes)
}
