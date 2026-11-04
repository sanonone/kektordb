// Package llm provides token accounting for every LLM consumer in the process.
//
// Why this exists: KektorDB keeps adding autonomy that depends on the configured
// LLM — Gardener (consolidation, contradictions, core facts, epistemic),
// compiler, RAG pipeline. Each of those has a cost the operator cannot see
// today: the client discarded the `usage` block and nobody counted tokens.
//
// This file makes the cost observable per feature, so a decision like "is
// contradiction detection worth its cost?" can be answered with numbers
// instead of guesses.
package llm

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Feature names are the labels used in the usage report. They are kept as
// constants so a typo in one call site cannot silently create a new bucket
// that never shows the expected data.
const (
	FeatureGardenerConsolidation = "gardener.consolidation"
	FeatureGardenerContradiction = "gardener.contradiction"
	FeatureGardenerCoreFacts     = "gardener.core_facts"
	FeatureGardenerEpistemic     = "gardener.epistemic"
	FeatureGardenerProfiles      = "gardener.profiles"
	FeatureGardenerKnowledgeGaps = "gardener.knowledge_gaps"
	FeatureGardenerPreferences   = "gardener.preferences"
	FeatureGardenerFailures      = "gardener.failures"
	FeatureGardenerEvolution     = "gardener.evolution"
	FeatureGardenerSentiment     = "gardener.sentiment"
	FeatureGardenerEntities      = "gardener.entities"
	FeatureGardenerOther         = "gardener.other"
	FeatureCompiler              = "compiler"
	FeatureRAG                   = "rag"
	FeatureProxy                 = "proxy"
	FeatureOther                 = "other"
)

// FeatureUsage is the accumulated cost of one feature.
type FeatureUsage struct {
	Calls        int64 `json:"calls"`
	Errors       int64 `json:"errors"`
	PromptTokens int64 `json:"prompt_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	// TotalMillis is the wall time spent waiting for the provider: useful to
	// separate "expensive" from "slow", which are different problems.
	TotalMillis int64 `json:"total_millis"`
}

// Total returns the combined token count.
func (f FeatureUsage) Total() int64 { return f.PromptTokens + f.OutputTokens }

// UsageSnapshot is a point-in-time view of the whole process.
type UsageSnapshot struct {
	Features []FeatureUsageEntry `json:"features"`
	Total    FeatureUsage        `json:"total"`
	// ProviderTokensReported is false when the provider never returned a
	// usage block: calls are counted but tokens are unknown. Reporting zero
	// tokens as if they were measured would make the cost look smaller than
	// it is.
	ProviderTokensReported bool `json:"provider_tokens_reported"`
}

// FeatureUsageEntry is one row of the snapshot.
type FeatureUsageEntry struct {
	Feature string `json:"feature"`
	FeatureUsage
}

// Recorder accumulates per-feature LLM usage. It is safe for concurrent use:
// the Gardener, the compiler and request handlers can all call it.
type Recorder struct {
	mu       sync.RWMutex
	features map[string]*featureCounters
}

type featureCounters struct {
	calls        atomic.Int64
	errors       atomic.Int64
	promptTokens atomic.Int64
	outputTokens atomic.Int64
	totalMillis  atomic.Int64
}

// Default is the process-wide recorder. A single instance is what makes the
// report meaningful: per-component recorders would each show a fraction.
var Default = NewRecorder()

// NewRecorder creates an empty recorder.
func NewRecorder() *Recorder {
	return &Recorder{features: make(map[string]*featureCounters)}
}

func (r *Recorder) bucket(feature string) *featureCounters {
	if feature == "" {
		feature = FeatureOther
	}
	r.mu.RLock()
	b, ok := r.features[feature]
	r.mu.RUnlock()
	if ok {
		return b
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if b, ok := r.features[feature]; ok {
		return b
	}
	b = &featureCounters{}
	r.features[feature] = b
	return b
}

// RecordCall registers one successful call.
//
// promptTokens/outputTokens are 0 when the provider did not report usage; the
// snapshot then flags that tokens are unknown instead of implying "free".
func (r *Recorder) RecordCall(feature string, promptTokens, outputTokens int, elapsedMillis int64) {
	b := r.bucket(feature)
	b.calls.Add(1)
	if promptTokens > 0 {
		b.promptTokens.Add(int64(promptTokens))
	}
	if outputTokens > 0 {
		b.outputTokens.Add(int64(outputTokens))
	}
	if elapsedMillis > 0 {
		b.totalMillis.Add(elapsedMillis)
	}
}

// RecordError registers a failed call. Errors still cost time (and sometimes
// tokens, when the provider fails after billing), so they are kept separate
// from successes rather than merged.
func (r *Recorder) RecordError(feature string, elapsedMillis int64) {
	b := r.bucket(feature)
	b.errors.Add(1)
	if elapsedMillis > 0 {
		b.totalMillis.Add(elapsedMillis)
	}
}

// Snapshot returns the current state, sorted by total tokens (descending) so
// the most expensive feature is first.
func (r *Recorder) Snapshot() UsageSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	snap := UsageSnapshot{}
	var anyTokens bool
	for name, b := range r.features {
		entry := FeatureUsageEntry{
			Feature: name,
			FeatureUsage: FeatureUsage{
				Calls:        b.calls.Load(),
				Errors:       b.errors.Load(),
				PromptTokens: b.promptTokens.Load(),
				OutputTokens: b.outputTokens.Load(),
				TotalMillis:  b.totalMillis.Load(),
			},
		}
		if entry.Total() > 0 {
			anyTokens = true
		}
		snap.Features = append(snap.Features, entry)
		snap.Total.Calls += entry.Calls
		snap.Total.Errors += entry.Errors
		snap.Total.PromptTokens += entry.PromptTokens
		snap.Total.OutputTokens += entry.OutputTokens
		snap.Total.TotalMillis += entry.TotalMillis
	}

	sort.Slice(snap.Features, func(i, j int) bool {
		if snap.Features[i].Total() != snap.Features[j].Total() {
			return snap.Features[i].Total() > snap.Features[j].Total()
		}
		return snap.Features[i].Calls > snap.Features[j].Calls
	})
	snap.ProviderTokensReported = anyTokens
	return snap
}

// Reset clears every counter. Useful to measure a specific window instead of
// the whole process lifetime.
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.features = make(map[string]*featureCounters)
}

// ------------------------------------------------------------------ context

// featureKey is the private context key for the feature label.
type featureKey struct{}

// WithFeature returns a context carrying the feature label that subsequent
// Chat calls should be attributed to. Callers set it once per operation:
//
//	ctx = llm.WithFeature(ctx, llm.FeatureGardenerContradiction)
//
// Keeping the label in the context (rather than adding a parameter to Client)
// avoids breaking every call site and every mock, and it works for the whole
// call tree below that point.
func WithFeature(ctx context.Context, feature string) context.Context {
	return context.WithValue(ctx, featureKey{}, feature)
}

// FeatureFromContext returns the feature label, or FeatureOther when unset.
func FeatureFromContext(ctx context.Context) string {
	if ctx == nil {
		return FeatureOther
	}
	if v, ok := ctx.Value(featureKey{}).(string); ok && v != "" {
		return v
	}
	return FeatureOther
}

// ChatRecorded runs a call and attributes its cost to the feature in ctx.
//
// This is the entry point every consumer should use instead of Chat directly:
// it measures elapsed time, reads the usage block and updates the recorder.
// Callers that do not use it are invisible in the report, which is exactly the
// blind spot this file exists to remove.
func ChatRecorded(ctx context.Context, c Client, systemPrompt, userQuery string) (string, error) {
	feature := FeatureFromContext(ctx)
	start := time.Now()
	resp, err := c.Chat(systemPrompt, userQuery)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		Default.RecordError(feature, elapsed)
		return resp, err
	}

	// Usage is captured by the client's last response when the provider reports it.
	prompt, output := LastUsage()
	Default.RecordCall(feature, prompt, output, elapsed)
	return resp, nil
}
