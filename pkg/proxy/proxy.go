package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/engine"
	"github.com/sanonone/kektordb/pkg/llm"
)

// AIProxy sits between the client and the LLM.
type AIProxy struct {
	cfg              Config
	engine           *engine.Engine
	reverseProxy     *httputil.ReverseProxy
	llmClient        llm.Client       // Smart Brain
	fastLLMClient    llm.Client       // Fast Brain
	firewallPatterns []*regexp.Regexp // Compiled regex cache
}

// Structures for manipulating Chat request JSON
type chatRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func NewAIProxy(cfg Config, dbEngine *engine.Engine) (*AIProxy, error) {
	target, err := url.Parse(cfg.TargetURL)
	if err != nil {
		return nil, fmt.Errorf("invalid target URL: %w", err)
	}

	p := &AIProxy{
		cfg:          cfg,
		engine:       dbEngine,
		reverseProxy: httputil.NewSingleHostReverseProxy(target),
	}

	// Init Regex
	if err := p.initFirewall(); err != nil {
		return nil, err
	}

	// Initialize clients if RAG is enabled
	if cfg.RAGEnabled {
		fastConfig := cfg.FastLLM
		if fastConfig.BaseURL == "" {
			fastConfig = cfg.LLM
		}
		if fastConfig.BaseURL == "" {
			fastConfig = llm.DefaultConfig()
		}
		p.fastLLMClient = llm.NewClient(fastConfig)
		slog.Info("[Proxy] Fast LLM initialized", "model", fastConfig.Model)

		if cfg.RAGUseHyDe {
			mainConfig := cfg.LLM
			if mainConfig.BaseURL == "" {
				mainConfig = llm.DefaultConfig()
			}
			p.llmClient = llm.NewClient(mainConfig)
			slog.Info("[Proxy] Smart LLM initialized", "model", mainConfig.Model)
		}
	}

	return p, nil
}

func (p *AIProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	totalStart := time.Now()

	// Intercept Admin Routes
	if r.Method == http.MethodPost && r.URL.Path == "/cache/invalidate" {
		p.handleCacheInvalidate(w, r)
		return
	}

	// 1. Read Body
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	// 2. Extract Text
	lastQuery := extractPrompt(bodyBytes)
	isStreaming := checkStreaming(bodyBytes)

	// Automatic Task Filter
	if isSystemTask(lastQuery) {
		slog.Info("[Proxy] Passthrough for System Task")
		p.reverseProxy.ServeHTTP(w, r)
		return
	}

	if lastQuery == "" {
		// slog.Debug("[Proxy] Passthrough for Empty Query (Init/Ping)")
		p.reverseProxy.ServeHTTP(w, r)
		return
	}

	// --- FIREWALL (Static) ---
	// Check text BEFORE doing anything expensive (Embedding/RAG)
	if blocked, reason := p.checkStaticFirewall(lastQuery); blocked {
		slog.Warn("[Firewall] BLOCKED (Static)", "reason", reason, "query", limitStr(lastQuery, 50))
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{
			"error": reason,
		})
		return
	}

	slog.Info("NEW RAG REQUEST", "query", limitStr(lastQuery, 50))

	// Variable to hold the HyDe hypothesis text (if generated)
	textToEmbed := lastQuery
	// Variable holding the rewritten/cleaned user query
	refinedQuery := lastQuery

	// --- ADVANCED RAG PIPELINE ---
	if p.cfg.RAGEnabled && lastQuery != "" {

		t1 := time.Now()
		fullHistory := extractFullHistory(bodyBytes)
		slog.Info("[1/4] Rewriting Query", "history_len", len(fullHistory))

		// STAGE 1: QUERY REWRITING
		if len(fullHistory) > 1 {
			slog.Debug("[RAG] Rewriting query using context...")
			rw, err := p.rewriteQuery(fullHistory)
			if err == nil && rw != "" {
				refinedQuery = rw
				textToEmbed = rw // Default: embed the rewritten query
				slog.Info("Rewritten", "duration", time.Since(t1), "original", limitStr(lastQuery, 30), "rewritten", limitStr(refinedQuery, 50))
			} else {
				slog.Warn("Rewrite skipped/failed", "duration", time.Since(t1), "error", err)
			}
		}

		// STAGE 2: GROUNDED HYDE
		if p.cfg.RAGUseHyDe && p.llmClient != nil {
			t2 := time.Now()
			slog.Info("[2/4] Grounding Search (Pre-search)...")

			// Grounding with rewritten query
			groundingVec, _ := p.cfg.Embedder.Embed(refinedQuery)

			if groundingVec != nil {
				// Use a lightweight search to find context
				snippets, _ := p.engine.VSearchGraph(p.cfg.RAGIndex, groundingVec, 20, "", "", 100, 0.5, nil, true, nil)

				slog.Debug("Found snippets for grounding", "count", len(snippets))

				var snippetText strings.Builder
				for _, s := range snippets {
					content := getTextFromMeta(s.Node.VectorData.Metadata)
					if len(content) > 1000 {
						content = content[:1000] + "..."
					}
					content = strings.ReplaceAll(content, "\n", " ")
					snippetText.WriteString("- " + content + "\n")
				}

				// Hypothesis generation only if grounding was found
				if snippetText.Len() > 0 {
					slog.Info("[3/4] Generating HyDe Hypothesis...")
					hypo, err := p.generateGroundedHyDe(refinedQuery, snippetText.String())
					if err == nil && hypo != "" {
						textToEmbed = hypo // Now we will embed the hypothesis
						slog.Info("Hypothesis generated", "duration", time.Since(t2), "chars", len(hypo))
						slog.Debug("Preview", "text", limitStr(hypo, 100))
					} else {
						slog.Warn("HyDe generation failed", "error", err)
					}
				} else {
					slog.Warn("Grounding found no context. HyDe might drift.")
				}
			}
		}
	}

	// STAGE 3: VECTOR CALCULATION (Dual Vector Strategy)
	// Calculate both "Original" (safe) and "HyDe" (experimental) vectors
	t4 := time.Now()
	slog.Info("[4/4] Embedding Strategy...")

	var originalVec []float32
	var hydeVec []float32

	// 1. Original Vector Calculation (always useful as fallback or firewall)
	originalVec, _ = p.cfg.Embedder.Embed(refinedQuery)

	// 2. HyDe Vector Calculation (only if different text was generated)
	if textToEmbed != "" && textToEmbed != refinedQuery {
		v, err := p.cfg.Embedder.Embed(textToEmbed)
		if err == nil {
			hydeVec = v
			slog.Debug("HyDe Vector computed")
		}
	}
	slog.Info("Embedding phase completed", "duration", time.Since(t4))

	// FIREWALL CHECK (Use originalVec for safety)
	if p.cfg.FirewallEnabled && len(originalVec) > 0 {
		if blocked, reason := p.checkFirewallWithVec(originalVec); blocked {
			slog.Warn("[Firewall] BLOCKED (Semantic)", "reason", reason)
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("Blocked by Semantic Firewall: %s", reason),
			})
			return
		}
	}

	var sourceIDs []string // Variable to hold sources

	// STAGE 4: RAG INJECTION WITH FALLBACK
	if p.cfg.RAGEnabled && strings.Contains(r.URL.Path, "/chat/completions") {
		tRag := time.Now()
		slog.Info("[RAG] Injecting Context...")

		var finalBody []byte
		var errInjection error
		usedStrategy := "Standard"

		// ATTEMPT 1: Use HyDe (if available)
		if len(hydeVec) > 0 {
			slog.Debug("Attempt 1: Using HyDe Vector...")
			finalBody, sourceIDs, errInjection = p.performRAGInjection(bodyBytes, hydeVec, refinedQuery)
			if finalBody != nil {
				usedStrategy = "HyDe"
			}
		}

		// ATTEMPT 2: Fallback to Original (Safety Net)
		// If HyDe was missing OR failed (nil body), use original
		if finalBody == nil && len(originalVec) > 0 {
			if len(hydeVec) > 0 {
				slog.Warn("HyDe yielded no results. Fallback to Original Vector")
			} else {
				slog.Debug("Attempt 1: Using Standard Search (HyDe skipped)")
			}
			finalBody, sourceIDs, errInjection = p.performRAGInjection(bodyBytes, originalVec, refinedQuery)
			usedStrategy = "Fallback/Standard"
		}

		if errInjection == nil && finalBody != nil {
			bodyBytes = finalBody
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			r.ContentLength = int64(len(bodyBytes))
			r.Header.Set("Content-Length", strconv.Itoa(len(bodyBytes)))
			slog.Info("Context Injected", "strategy", usedStrategy, "duration", time.Since(tRag))
		} else {
			slog.Warn("CRITICAL: No context found even after fallback. LLM will answer blindly")
		}
	}

	// 4. CACHE READ
	// Use originalVec for cache to maximize hits on similar questions
	if !isStreaming && p.cfg.CacheEnabled && len(originalVec) > 0 {
		if cachedResp, hit := p.checkCache(originalVec); hit {
			slog.Info("[Cache] HIT")
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Kektor-Cache", "HIT")
			w.Write([]byte(cachedResp))
			return
		}
	}

	// 5. FORWARD
	if isStreaming || !p.cfg.CacheEnabled || len(originalVec) == 0 {
		p.reverseProxy.ServeHTTP(w, r)
		return
	}

	capturer := &responseCapturer{
		ResponseWriter: w,
		body:           new(bytes.Buffer),
		statusCode:     http.StatusOK,
	}

	p.reverseProxy.ServeHTTP(capturer, r)

	slog.Info("REQUEST COMPLETED", "duration", time.Since(totalStart))

	if capturer.statusCode == http.StatusOK {
		go p.saveToCache(originalVec, lastQuery, capturer.body.Bytes(), sourceIDs)
	}
}

// --- HELPER FUNCTIONS ---

func isSystemTask(text string) bool {
	// Common patterns from Open WebUI for automatic tasks
	if strings.Contains(text, "### Task:") {
		return true
	}
	if strings.Contains(text, "Generate a concise") && strings.Contains(text, "title") {
		return true
	}
	if strings.Contains(text, "Generate 1-3 broad tags") {
		return true
	}
	if strings.Contains(text, "Suggest 3-5 relevant follow-up") {
		return true
	}
	return false
}

func (p *AIProxy) rewriteQuery(history []llm.Message) (string, error) {
	maxHist := 4
	if len(history) > maxHist {
		history = history[len(history)-maxHist:]
	}
	var chatTxt strings.Builder
	for _, msg := range history {
		role := "User"
		if msg.Role == "assistant" {
			role = "Assistant"
		}
		chatTxt.WriteString(fmt.Sprintf("%s: %s\n", role, msg.Content))
	}
	sysPrompt := p.cfg.RAGRewriterPrompt
	return p.fastLLMClient.Chat(sysPrompt, chatTxt.String())
}

func (p *AIProxy) generateGroundedHyDe(query string, snippets string) (string, error) {
	sysPrompt := p.cfg.RAGGroundedHyDePrompt
	if strings.Contains(sysPrompt, "{{context}}") {
		sysPrompt = strings.ReplaceAll(sysPrompt, "{{context}}", snippets)
	} else {
		sysPrompt += "\nContext:\n" + snippets
	}
	return p.llmClient.Chat(sysPrompt, query)
}

func extractFullHistory(jsonBody []byte) []llm.Message {
	var data map[string]interface{}
	_ = json.Unmarshal(jsonBody, &data)
	var res []llm.Message
	if msgs, ok := data["messages"].([]interface{}); ok {
		for _, m := range msgs {
			if mMap, ok := m.(map[string]interface{}); ok {
				role, _ := mMap["role"].(string)
				content, _ := mMap["content"].(string)
				res = append(res, llm.Message{Role: role, Content: content})
			}
		}
	}
	return res
}

func checkStreaming(body []byte) bool {
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return false
	}
	if val, ok := data["stream"].(bool); ok {
		return val
	}
	return false
}

func (p *AIProxy) checkFirewallWithVec(vec []float32) (bool, string) {
	results, err := p.engine.VSearchWithScores(p.cfg.FirewallIndex, vec, 1)
	if err != nil || len(results) == 0 {
		return false, ""
	}
	bestMatch := results[0]
	if float32(bestMatch.Score) < p.cfg.FirewallThreshold {
		return true, fmt.Sprintf("Similar to '%s' (Dist: %.4f)", bestMatch.ID, bestMatch.Score)
	}
	return false, ""
}

func (p *AIProxy) checkCache(vec []float32) (string, bool) {
	results, err := p.engine.VSearchWithScores(p.cfg.CacheIndex, vec, 1)
	if err != nil || len(results) == 0 {
		return "", false
	}
	best := results[0]
	if float32(best.Score) < p.cfg.CacheThreshold {
		data, err := p.engine.VGet(p.cfg.CacheIndex, best.ID)
		if err == nil {
			if createdAt, ok := data.Metadata["created_at"].(float64); ok {
				createdTime := time.Unix(int64(createdAt), 0)
				if p.cfg.CacheTTL > 0 && time.Since(createdTime) > p.cfg.CacheTTL {
					slog.Info("[Cache] Item expired. Cleanup.")
					go func(id string) { _ = p.engine.VDelete(p.cfg.CacheIndex, id) }(best.ID)
					return "", false
				}
			}
			if resp, ok := data.Metadata["response"].(string); ok {
				return resp, true
			}
		}
	}
	return "", false
}

func (p *AIProxy) saveToCache(queryVec []float32, queryText string, responseBytes []byte, sourceIDs []string) {
	if len(responseBytes) == 0 {
		return
	}
	info, err := p.engine.DB.GetSingleVectorIndexInfoAPI(p.cfg.CacheIndex)
	if err == nil {
		if p.cfg.MaxCacheItems > 0 && info.VectorCount >= p.cfg.MaxCacheItems {
			return
		}
	}

	// Join IDs into a space-separated string for Full-Text Search indexing
	// e.g. "chunk_1 chunk_5 chunk_9"
	sourcesStr := strings.Join(sourceIDs, " ")

	id := fmt.Sprintf("cache_%d_%d", time.Now().UnixNano(), len(queryText))
	meta := map[string]interface{}{
		"query":      queryText,
		"response":   string(responseBytes),
		"created_at": float64(time.Now().Unix()),
		"sources":    sourcesStr, // for invalidation cache
	}
	maintConfig := &hnsw.AutoMaintenanceConfig{
		VacuumInterval:  hnsw.Duration(p.cfg.CacheVacuumInterval),
		DeleteThreshold: p.cfg.CacheDeleteThreshold,
		RefineEnabled:   false,
	}
	_ = p.engine.VCreate(p.cfg.CacheIndex, distance.Cosine, 16, 200, distance.Float32, "", maintConfig, nil)
	p.engine.VAdd(p.cfg.CacheIndex, id, queryVec, meta)
	slog.Info("[Cache] Saved")
}

func (p *AIProxy) checkFirewall(text string) (bool, string) {
	vec, err := p.cfg.Embedder.Embed(text)
	if err != nil {
		return false, ""
	}
	results, err := p.engine.VSearchWithScores(p.cfg.FirewallIndex, vec, 1)
	if err != nil || len(results) == 0 {
		return false, ""
	}
	bestMatch := results[0]
	if float32(bestMatch.Score) < p.cfg.FirewallThreshold {
		return true, fmt.Sprintf("Similar to known threat '%s'", bestMatch.ID)
	}
	return false, ""
}

func extractPrompt(jsonBody []byte) string {
	var data map[string]interface{}
	if err := json.Unmarshal(jsonBody, &data); err != nil {
		return ""
	}
	if v, ok := data["prompt"].(string); ok {
		return v
	}
	if msgs, ok := data["messages"].([]interface{}); ok {
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgMap, ok := msgs[i].(map[string]interface{}); ok {
				if role, _ := msgMap["role"].(string); role == "user" {
					if content, _ := msgMap["content"].(string); content != "" {
						return content
					}
				}
			}
		}
	}
	return ""
}

func (p *AIProxy) performRAGInjection(originalBody []byte, queryVec []float32, queryText string) ([]byte, []string, error) {
	filter := ""
	hybridQuery := ""
	if p.cfg.RAGUseHybrid {
		hybridQuery = queryText
	}

	alpha := 0.5
	if p.cfg.RAGHybridAlpha != 0 {
		alpha = p.cfg.RAGHybridAlpha
	}
	if !p.cfg.RAGUseHybrid {
		alpha = 1.0
	}

	efSearch := p.cfg.RAGEfSearch
	if efSearch <= 0 {
		efSearch = 100
	}

	var relations []string
	if p.cfg.RAGUseGraph {
		relations = []string{"prev", "next", "parent", "mentions", "mentioned_in"}
	}

	results, err := p.engine.VSearchGraph(p.cfg.RAGIndex, queryVec, p.cfg.RAGTopK, filter, hybridQuery, efSearch, alpha, relations, true, nil)
	if err != nil || len(results) == 0 {
		return nil, nil, nil
	}

	slog.Debug("Found snippets for grounding", "count", len(results))

	var contextBuilder strings.Builder
	foundRelevant := false
	seenContent := make(map[string]struct{})

	var usedSourceIDs []string

	for _, res := range results {
		if float32(res.Score) < p.cfg.RAGThreshold {
			continue
		}

		mainText := getTextFromMeta(res.Node.VectorData.Metadata)
		if mainText == "" {
			continue
		}

		mainText = strings.ReplaceAll(mainText, "](/assets/", "](http://localhost:9091/assets/")

		prevText := ""
		if prevNodes, ok := res.Node.Connections["prev"]; ok && len(prevNodes) > 0 {
			prevText = getTextFromMeta(prevNodes[0].VectorData.Metadata)
		}
		nextText := ""
		if nextNodes, ok := res.Node.Connections["next"]; ok && len(nextNodes) > 0 {
			nextText = getTextFromMeta(nextNodes[0].VectorData.Metadata)
		}

		sourceName := "Unknown Source"
		if parents, ok := res.Node.Connections["parent"]; ok && len(parents) > 0 {
			pMeta := parents[0].VectorData.Metadata
			if name, ok := pMeta["filename"].(string); ok {
				sourceName = name
			} else if src, ok := pMeta["source"].(string); ok {
				sourceName = filepath.Base(src)
			}
		}

		var topics []string
		if entityNodes, ok := res.Node.Connections["mentions"]; ok {
			for _, en := range entityNodes {
				if name, ok := en.VectorData.Metadata["name"].(string); ok {
					topics = append(topics, name)
				}
			}
		}

		usedSourceIDs = append(usedSourceIDs, res.ID)

		blockHash := sourceName + mainText
		if _, exists := seenContent[blockHash]; exists {
			continue
		}
		seenContent[blockHash] = struct{}{}

		contextBuilder.WriteString(fmt.Sprintf("--- Source Document: %s ---\n", sourceName))
		if len(topics) > 0 {
			contextBuilder.WriteString(fmt.Sprintf("[Related Topics: %s]\n", strings.Join(topics, ", ")))
		}
		if prevText != "" {
			contextBuilder.WriteString(prevText + " ")
		}
		contextBuilder.WriteString(mainText)
		if nextText != "" {
			contextBuilder.WriteString(" " + nextText)
		}
		contextBuilder.WriteString("\n\n")
		foundRelevant = true

		// Debug
		if strings.Contains(mainText, "![") {
			slog.Info("[DEBUG IMAGE] Found image tag in chunk", "content", mainText)
		}
	}

	if !foundRelevant {
		return nil, nil, nil
	}

	promptTemplate := p.cfg.RAGSystemPrompt
	if promptTemplate == "" {
		promptTemplate = "Context:\n{{context}}\nQuestion:\n{{query}}"
	}

	finalContent := strings.ReplaceAll(promptTemplate, "{{context}}", contextBuilder.String())
	finalContent = strings.ReplaceAll(finalContent, "{{query}}", queryText)

	var requestData map[string]interface{}
	if err := json.Unmarshal(originalBody, &requestData); err != nil {
		return nil, nil, err
	}
	messages, ok := requestData["messages"].([]interface{})
	if !ok || len(messages) == 0 {
		return nil, nil, nil
	}
	lastMsgIdx := len(messages) - 1
	lastMsg, ok := messages[lastMsgIdx].(map[string]interface{})
	if !ok {
		return nil, nil, nil
	}
	if _, ok := lastMsg["content"].(string); ok {
		lastMsg["content"] = finalContent
		messages[lastMsgIdx] = lastMsg
		requestData["messages"] = messages
	} else {
		return nil, nil, nil
	}

	reqD, err := json.Marshal(requestData)
	if err != nil {
		return nil, nil, err
	}

	return reqD, usedSourceIDs, nil
}

func getTextFromMeta(meta map[string]any) string {
	if v, ok := meta["content"].(string); ok {
		return v
	}
	if v, ok := meta["text"].(string); ok {
		return v
	}
	if v, ok := meta["page_content"].(string); ok {
		return v
	}
	return ""
}

func limitStr(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

type InvalidateRequest struct {
	DocumentID string `json:"document_id"`
}

func (p *AIProxy) handleCacheInvalidate(w http.ResponseWriter, r *http.Request) {
	var req InvalidateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.DocumentID == "" {
		http.Error(w, "document_id required", http.StatusBadRequest)
		return
	}

	// LOGIC: Find cache entries where 'sources' field contains the DocumentID.
	// We use the Engine's text search capabilities (BM25/Inverted Index).
	// The Cache Index is p.cfg.CacheIndex (usually "semantic_cache").

	// 1. Search for IDs
	// "sources" is the field name we used in saveToCache
	results, _ := p.engine.DB.FindIDsByTextSearch(p.cfg.CacheIndex, "sources", req.DocumentID)

	if len(results) == 0 {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "ok",
			"deleted": 0,
			"message": "No cache entries found for this document",
		})
		return
	}

	// 2. Delete them
	count := 0
	for _, res := range results {
		// We need ExternalID to delete via Engine.VDelete
		// FindIDsByTextSearch returns InternalIDs (uint32) in types.SearchResult
		// We need to resolve Internal -> External using HNSW index map.

		idx, _ := p.engine.DB.GetVectorIndex(p.cfg.CacheIndex)
		if hnswIdx, ok := idx.(*hnsw.Index); ok {
			if extID, found := hnswIdx.GetExternalID(res.DocID); found {
				_ = p.engine.VDelete(p.cfg.CacheIndex, extID)
				count++
			}
		}
	}

	slog.Info("[Cache] Invalidated entries", "reason", "dependency_change", "doc_id", req.DocumentID, "count", count)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"deleted": count,
	})
}
