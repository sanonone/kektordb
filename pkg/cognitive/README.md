# Module: pkg/cognitive

## Purpose

An autonomous background "reflection engine" (the `Gardener`) that continuously analyzes KektorDB's knowledge graph while the system is idle or under heavy write load. Performs tasks analogous to human REM sleep: consolidating redundant memories, detecting contradictions, discovering implicit relationships, tracking sentiment shifts, and building user profiles. Operates in three modes (basic/advanced/meta) with graceful degradation when no LLM is available.

## Key Types & Critical Paths

**Critical structs:**
- `Gardener` -- Reflection engine: `eng *engine.Engine`, `llm llm.Client`, `cfg Config`, `stopCh`, `eventCh chan engine.Event` (buffered: 64), `scanCursors map[string]uint32` (protected by `cursorsMu`), `writeCounter atomic.Int64`, `lastThinkTime time.Time` (protected by `lastThinkMu`), `newReflections []string` (protected by `reflectionsMu`), `unassimilatedInteractions map[string]int` (protected by `profileMutex`).
- `Config` -- Master config: `Enabled`, `Interval`, `Mode` (basic/advanced/meta), `TargetIndexes`, `AdaptiveThreshold` (default: 50), `AdaptiveMinInterval` (default: 30s), `AutoResolveEnabled`, `MemoryConfig`, `EnableUserProfiling`, `EnableCoreFactExtraction`, `CoreFactMinConfidence` (default: 0.85).
- `SentimentLexicon` -- `{Positive, Negative []string}` per language. English: 8+8 roots; Italian: 8+8 roots.

**Critical paths (hot functions):**
- `think()` -- Main entry point: iterates indexes, runs detectors in fixed pipeline order. Called by ticker, adaptive trigger, or `ForceThink()`.
- `loop()` -- Background goroutine: `select` on `stopCh`, `eventCh`, `ticker.C`. Increments `writeCounter`, launches adaptive thinks.
- `consolidateCluster()` -- Finds redundant memories (similarity >= 0.90, >= 5 members), synthesizes summary (LLM or deterministic), averages vectors, transfers edges, archives old nodes.
- `detectCrossValidator()` -- Meta-mode: builds `entityID -> detectorType -> reflectionIDs` map, creates composite reflections with geometric mean confidence + diversity boost.

## Architecture & Data Flow

**Three operational modes:**
- **basic** (no LLM required): Knowledge gaps, importance shifts, sentiment shifts, centrality shifts, forgetting patterns, memory consolidation, redundant cluster detection.
- **advanced** (LLM required): All basic + contradiction detection, user preferences, repeated failures, knowledge evolution (time-travel subgraph snapshots), core fact extraction.
- **meta** (LLM required): All advanced + cross-detector confidence validation (composite reflections when 2+ detector types flag the same entity).

**Core Fact Extraction:**
When `EnableCoreFactExtraction` is true, the Gardener analyzes `user_interaction` and `episodic` memories to extract immutable facts about users (name, profession, preferences, constraints). Creates `type="core_fact"` nodes with:
- `_pinned=true` metadata to bypass time-decay
- `extracted_from` edges linking to source memories
- Confidence threshold filtering (default: 0.85)

Extracted facts are automatically linked to the source memories and can be resolved by the existing contradiction detection system when conflicts arise (e.g., user changes profession).

**Think cycle pipeline:** Iterates over all allowed vector indexes, running detectors in a fixed order. Each detector uses cursor-based incremental scanning (500 IDs per call) so large databases are processed across multiple cycles rather than all at once. Every LLM-based analysis marks analyzed pairs/entities with graph edges (`analyzed_against`, `gap_analyzed`, `sentiment_analyzed`, etc.) to prevent re-processing in subsequent cycles.

**Memory consolidation:** `findRedundantClusters()` uses vector similarity search (top-10, threshold >= 0.90) to find duplicate memories. Clusters must have >= 5 members. If an LLM is available, it synthesizes a summary; otherwise `pickCentralContent()` selects the most graph-connected member as the "master" (deterministic fallback). The consolidated memory's embedding is the arithmetic mean of all member vectors -- "zero-cost embedding." All outgoing/incoming edges are transferred to the master with deduplication; old memories are marked `_archived=true` and linked via `consolidated_into` edges.

**Sentiment analysis:** Uses substring matching on word roots from a language-specific lexicon (English: 8 positive + 8 negative roots; Italian: 8 positive + 8 negative roots). Scans entities with >= 4 incoming "mentions" edges, splits mentions into "past" (>14 days) and "recent" (<=14 days) buckets, triggers a reflection if `|avgRecent - avgPast| >= 1.5`.

**Adaptive triggering:** Dual-trigger scheduling -- periodic ticker (default: 10 min) AND write-driven trigger. The `onEvent()` handler increments `writeCounter`; when it reaches `AdaptiveThreshold` (default: 50) AND at least `AdaptiveMinInterval` (default: 30s) has passed since the last think, it launches `think()` in a new goroutine.

**Cross-detector validation (meta mode):** Builds a map of `entityID -> detectorType -> reflectionIDs`. When an entity is flagged by 2+ detector types, creates a composite reflection with confidence: `min(1.0, geometricMean(individualConfidences) * diversityBoost)` where `diversityBoost = 0.7 + 0.3 * min(1.0, detectorCount/3.0)`.

## Cross-Module Dependencies

**Depends on:**
- `pkg/engine` -- Primary dependency. Uses VSearch, VLink, VUnlink, VGetLinks, VGetRelations, VReinforce, VGet, VGetMany for all reflection analysis and memory operations.
- `pkg/llm` -- Optional LLM client for advanced/meta mode detectors (contradiction, preferences, failures, evolution, consolidation synthesis).
- `pkg/core` -- Indirectly via engine. Access to vector indexes and graph structure.

**Used by:**
- `internal/server` -- Creates Gardener on startup if cognitive config is loaded. Exposes `/vector/indexes/{name}/cognitive/think` endpoint for manual trigger.
- `internal/mcp` -- `CheckSubconscious`, `ResolveConflict`, `AskMetaQuestion` tools query the Gardener's reflection nodes in the database.
- `cmd/kektordb/main.go` -- Passes cognitive config to server, which creates the Gardener.

## Concurrency & Locking Rules

**Background loop:** Single goroutine (`loop()`) multiplexing ticker, events, and stop signal via `select`. Event bus subscription is a buffered channel (size 64) -- events can queue up but will not block the engine.

**Adaptive think in separate goroutine:** `go g.think()` from the event handler -- non-blocking. The periodic ticker also calls `think()` directly. This means multiple concurrent `think()` calls are possible (ticker + adaptive + manual `ForceThink`).

**User interaction tracking:** Protected by `sync.RWMutex` (`profileMutex`) around the `unassimilatedInteractions` map. User profile updates launch in separate goroutines (`go g.UpdateUserProfile(...)`) when threshold is reached.

**Thread-safe state access:** All shared mutable state is now protected:
- `scanCursors` -- Protected by `cursorsMu sync.RWMutex`. Use `getCursor()` and `setCursor()` helpers.
- `lastThinkTime` -- Protected by `lastThinkMu sync.RWMutex`. Use `getLastThinkTime()` and `setLastThinkTime()` helpers.
- `writeCounter` -- Uses `atomic.Int64` for lock-free increments.
- `newReflections` -- Protected by `reflectionsMu sync.Mutex`.

## Known Pitfalls / Gotchas

- **Concurrent `think()` calls can process overlapping data** -- While cursor state is now protected by mutexes, concurrent thinks may still process overlapping node ranges if they start at similar times. This is harmless (just slightly wasteful) since dedup edges prevent duplicate analysis.
- **LLM calls have no rate limiting** -- Each contradiction pair, failure group, and evolution entity triggers a separate `llm.Chat()` call with no inter-call delay. For large databases with many flagged entities, this can overwhelm the LLM API.
- **Lexicon substring matching produces false positives** -- `"good"` matches `"goodbye"`, `"ottim"` matches `"ottimizzare"` (which means "to optimize", not "excellent"). No negation handling ("not great" scores as positive).
- **Hardcoded thresholds everywhere** -- 0.90 similarity, 5 min cluster size, 1.5 sentiment delta, 3x centrality growth, 30-day windows, 14-day sentiment split. None are runtime configurable.
- **`embedContent()` is a stub** -- Session summarization has a TODO for proper embedding support, currently falling back to zero vectors. Consolidated memories have zero-vector embeddings until this is fixed.
- **Cursor state loss on restart** -- `scanCursors` is in-memory only. On process restart, all detectors start from position 0. Dedup edges prevent re-analysis but the first cycle after restart is wasteful.

## Design Trade-offs

| Trade-off | Decision | Rationale |
|---|---|---|
| **Fine-grained locking for shared state** | `scanCursors` (RWMutex), `lastThinkTime` (RWMutex), `writeCounter` (atomic), `newReflections` (Mutex) | Correctness over simplicity; minimal overhead since operations are rare (10min intervals) |
| **Cursor-based incremental scanning** | 500 IDs per detector per cycle | Large databases are processed incrementally across cycles rather than blocking for minutes |
| **Graph-as-memory for reflections** | Reflections are first-class nodes in the vector+graph | Makes reflections searchable, linkable, and embeddable; enables cross-detector validation |
| **Deterministic fallback for every LLM feature** | Works without an LLM | Graceful degradation; the system is functional even with `mode: "basic"` |
| **Lexicon-based sentiment analysis** | Substring matching on word roots | Zero dependencies, fast; produces false positives (e.g., "goodbye" matches "good") |
| **Hardcoded thresholds** | 0.90 similarity, 5 min cluster size, 1.5 sentiment delta, 30-day windows | Simplicity; not runtime configurable |
| **Cursor state loss on restart** | `scanCursors` is in-memory only | Dedup edges mitigate re-analysis; cursor persistence is a TODO |
