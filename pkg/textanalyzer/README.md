# Module: pkg/textanalyzer

## Purpose

A pluggable text analysis pipeline for full-text search indexing. Tokenizes text, removes language-specific stop words, and applies stemming algorithms (Porter2 for English, Snowball for Italian) to reduce words to their root forms. Both stemmers implement the `Analyzer` interface, enabling polymorphic selection at runtime based on the index's configured language.

## Key Types & Critical Paths

**Critical types:**
- `Analyzer` (interface) -- Single method: `Analyze(text string) []string`. Implemented by `EnglishStemmer` and `ItalianStemmer`.
- `EnglishStemmer` / `ItalianStemmer` -- Empty structs (`struct{}`) serving as type markers. All logic is in method receivers.
- `tokenizerRegex` -- Package-level `*regexp.Regexp`, compiled once at init: `[\p{L}0-9_]+`.

**Critical paths (hot functions):**
- `Tokenize()` -- Lowercase + regex extract. Pre-compiled regex, no recompilation per call.
- `FilterEnglishStopWords()` / `FilterItalianStopWords()` -- O(1) map lookups via `map[string]struct{}`. Pre-allocated result slice.
- `StemEnglish()` -- 9-step pipeline: Step0 (possessive) -> Step1a (plurals) -> Step1b (-ed/-ing) -> Step1c (y->i) -> Steps 2-4 (suffix tables) -> Step5 (cleanup). R1/R2 region constraints.
- `StemItalian()` -- Preprocess (accent normalization, intervocalic marking) -> Step0 (clitic pronouns) -> Step1/Step2 (nominal/verb suffixes, mutually exclusive) -> Step3 (final vowels) -> Postprocess (restore markers). R1/R2/RV regions.

## Architecture & Data Flow

**Shared pipeline (`analyzer.go`):** `Tokenize()` lowercases input, then extracts words matching `[\p{L}0-9_]+` (Unicode letters, digits, underscores) using a pre-compiled `regexp.Regexp`. Stop word filtering uses `map[string]struct{}` for O(1) lookups. English: 31 common words. Italian: ~130 words including full conjugations of `avere`, `essere`, and `stare`. Result slices are pre-allocated with `make([]string, 0, len(tokens))` to avoid reallocations.

**English Stemmer (Porter2/Snowball):** Pipeline per token: `Tokenize -> FilterStopWords -> Step0 -> Step1a -> Step1b -> Step1c -> Step2 -> Step3 -> Step4 -> Step5`. R1/R2 regions computed once per word -- R1 is the region after the first consonant following a vowel; R2 is the same but starting from R1. Suffix stripping is constrained to these regions to avoid over-stemming. Exception tables handle known edge cases (16 entries in `exceptions1`, 8 in `exceptions2`). Step 0 strips possessive `'s`/`s'`/trailing `'`. Step 1a handles plurals. Step 1b handles `-ed`/`-ing`/`-eed`. Steps 2-4 use progressive suffix replacement tables. Step 5 is final cleanup (trailing `e`, double `l`).

**Italian Stemmer (Snowball):** Pipeline: `Tokenize -> FilterStopWords -> Preprocess -> Step0 -> Step1/Step2 -> Step3 -> Postprocess`. More complex R1/R2/RV regions -- RV (vowel region) has three calculation rules based on the first 2-3 characters. Preprocessing normalizes accented vowels and marks intervocalic `i`/`u` by uppercasing them. Step 0 removes clitic pronouns (35 entries) from the RV region. Step 1 removes nominal/adverbial suffixes; Step 2 removes verb suffixes (64 entries) -- only executed if Step 1 made no changes (mutual exclusion). Step 3 removes residual final vowels. Post-processing restores uppercased `I`/`U` markers.

## Cross-Module Dependencies

**Depends on:**
- `regexp`, `strings`, `unicode` -- Standard library text processing.
- No external dependencies.

**Used by:**
- `pkg/core` -- BM25 full-text index uses the `Analyzer` interface for tokenization and stemming during indexing and search.
- `pkg/rag` -- Indirectly via core's full-text index when processing text documents.

## Concurrency & Locking Rules

**Fully stateless and goroutine-safe:** Both stemmers have no mutable shared state. All data flows through function parameters and local variables. The `Analyzer` structs are empty (`struct{}`), serving only as type markers for the interface. Multiple goroutines can safely call `Analyze()` on the same stemmer instance simultaneously.

**Pre-compiled regex:** `tokenizerRegex` is a package-level `var`, compiled once at init time. Safe for concurrent use by multiple goroutines.

**Stop word maps are read-only:** Populated at package initialization, never mutated. Safe for concurrent reads.

## Known Pitfalls / Gotchas

- **Italian test suite is skipped** -- `TestStemItalian` has `t.Skip()` with the message "Skipping Italian stemmer test temporarily." The Italian implementation may not be fully validated. Use with caution for production Italian text search.
- **Two identical `replaceSuffixIfInRegion` functions** -- The English and Italian stemmers have their own implementations that are functionally identical. This is code duplication. If you fix a bug in one, you must remember to fix it in the other.
- **No caching** -- Each call recomputes stemming from scratch. For documents with many repeated terms (e.g., technical docs with recurring keywords), a per-token LRU cache could improve performance.
- **String-to-[]rune conversions in Italian stemmer** -- The Italian stemmer converts to `[]rune` for region calculation, then back to `string` for suffix operations. This is correct for Unicode but has allocation overhead. For high-throughput indexing, this can contribute to GC pressure.
- **Substring matching for sentiment roots** -- Used by the cognitive gardener's lexicon. `"ottim"` matches `"ottimo"`, `"ottima"`, but also `"ottimizzare"` (to optimize). No negation handling: "not great" scores as positive.
- **`tokenizerRegex` matches underscores** -- The pattern `[\p{L}0-9_]+` includes underscores. This means `snake_case_variable` is treated as a single token, not split into `snake`, `case`, `variable`. This may or may not be desired depending on the use case.

## Design Trade-offs

| Trade-off | Decision | Rationale |
|---|---|---|
| **No caching** | Each call recomputes stemming from scratch | Simplicity; a per-token LRU cache could improve performance for repeated terms |
| **String-to-[]rune conversions** | Italian stemmer converts for region calculation | Correct for Unicode but has allocation overhead |
| **Two `replaceSuffixIfInRegion` functions** | English and Italian have identical implementations | Code duplication; could be consolidated into shared `analyzer.go` |
| **Italian test suite is skipped** | `TestStemItalian` has `t.Skip()` | Italian implementation may not be fully validated |
| **Substring matching for sentiment roots** | `"ottim"` matches `"ottimo"`, `"ottima"`, etc. | Lightweight but imprecise; could produce false positives |
| **Mutual exclusion in Italian Step1/Step2** | Step 2 only runs if Step 1 made no changes | Prevents double-stemming; correct Snowball algorithm behavior |
