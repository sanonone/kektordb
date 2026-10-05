// Package compiler provides deterministic and LLM-assisted artifact compilation
// for the KektorDB Knowledge Engine.
//
// It supports built-in templates (user_profile, project_summary, entity_card,
// conversation_context, topic_overview), field-level provenance tracking,
// asynchronous compilation with polling, multi-version artifact storage,
// and an autonomous Artifact Watcher that tracks staleness across indexes.
package compiler

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
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

	// wg tracks in-flight async compilations. Without it, a goroutine started
	// by StartAsyncCompile can outlive its caller and read vectors through
	// arena pointers that Engine.Close has since unmapped (SIGSEGV, C1).
	wg sync.WaitGroup

	// closing is set by Close so new async work is refused instead of starting
	// a goroutine that would race with shutdown.
	closing  atomic.Bool
	closeMu  sync.Mutex
	closedCh chan struct{}
}

// NewCompiler creates a new Compiler backed by the given engine.
// If llmClient is nil, only deterministic compilation is available.
// If embedder is nil, semantic search and artifact vector averaging are unavailable.
func NewCompiler(eng *engine.Engine, llmClient llm.Client, emb embeddings.Embedder) *Compiler {
	c := &Compiler{
		eng:         eng,
		llm:         llmClient,
		embedder:    emb,
		templates:   BuiltinTemplates,
		taskManager: newCompileTaskManager(),
		closedCh:    make(chan struct{}),
	}

	// Drain async compilations during Engine.Close, before arenas are unmapped.
	// Registered here so every caller is protected automatically, including
	// tests and embedded usage that never call Close explicitly (C1).
	if eng != nil {
		eng.RegisterCloseHook("compiler", c.Close)
	}

	return c
}

// Close stops the task manager and waits for in-flight async compilations to
// finish, so no goroutine keeps reading arena memory after the engine unmaps it
// (C1). Safe to call multiple times and from multiple goroutines.
//
// A timeout <= 0 waits indefinitely. Callers that cannot block should pass a
// bounded timeout and accept that a straggler may still be running.
func (c *Compiler) Close(timeout time.Duration) error {
	c.closeMu.Lock()
	if !c.closing.Swap(true) {
		close(c.closedCh)
	}
	c.closeMu.Unlock()

	if c.taskManager != nil {
		c.taskManager.Close()
	}

	// wg.Wait must not race with a concurrent wg.Add: StartAsyncCompile performs
	// its Add while holding closeMu, so once closing is set (above, under the
	// same lock) no further Add can happen -- every goroutine either is already
	// counted or was refused. Without this, Add during Wait is a data race.
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	if timeout <= 0 {
		<-done
		return nil
	}
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("compiler close timed out after %s waiting for async compilations", timeout)
	}
}

// isClosing reports whether Close has been called.
func (c *Compiler) isClosing() bool {
	return c.closing.Load()
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
// An explicitly-set TaskSpec policy always wins (even one that disables
// history — previously KeepHistory=false was silently ignored); otherwise the
// template policy; otherwise the built-in default (B3 fix).
func (c *Compiler) resolveRefreshPolicy(req CompileRequest, template *CompileTemplate) RefreshPolicy {
	if req.TaskSpec != nil && !IsZeroPolicy(req.TaskSpec.RefreshPolicy) {
		return req.TaskSpec.RefreshPolicy
	}
	if template != nil && !IsZeroPolicy(template.RefreshPolicy) {
		return template.RefreshPolicy
	}
	return DefaultRefreshPolicy()
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
