package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
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
	cfg           Config
	engine        *engine.Engine
	reverseProxy  *httputil.ReverseProxy
	llmClient     llm.Client // Smart Brain
	fastLLMClient llm.Client // Fast Brain
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

	// Inizializza i client se RAG è attivo
	if cfg.RAGEnabled {
		fastConfig := cfg.FastLLM
		if fastConfig.BaseURL == "" {
			fastConfig = cfg.LLM
		}
		if fastConfig.BaseURL == "" {
			fastConfig = llm.DefaultConfig()
		}
		p.fastLLMClient = llm.NewClient(fastConfig)
		log.Printf("[Proxy] Fast LLM initialized (Model: %s)", fastConfig.Model)

		if cfg.RAGUseHyDe {
			mainConfig := cfg.LLM
			if mainConfig.BaseURL == "" {
				mainConfig = llm.DefaultConfig()
			}
			p.llmClient = llm.NewClient(mainConfig)
			log.Printf("[Proxy] Smart LLM initialized (Model: %s)", mainConfig.Model)
		}
	}

	return p, nil
}

func (p *AIProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	totalStart := time.Now()

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

	// Filtro Task Automatici
	if isSystemTask(lastQuery) {
		log.Printf("[Proxy] ⏭️  Passthrough for System Task.")
		p.reverseProxy.ServeHTTP(w, r)
		return
	}

	log.Printf("\n=== 🚀 NEW RAG REQUEST: '%s' ===", limitStr(lastQuery, 50))

	// Variabile che conterrà il testo dell'ipotesi HyDe (se generata)
	textToEmbed := lastQuery
	// Variabile che contiene la query riscritta/pulita dell'utente
	refinedQuery := lastQuery

	// --- PIPELINE RAG AVANZATA ---
	if p.cfg.RAGEnabled && lastQuery != "" {

		t1 := time.Now()
		fullHistory := extractFullHistory(bodyBytes)
		log.Printf("[1/4] 🧠 Rewriting Query (History: %d msgs)...", len(fullHistory))

		// STAGE 1: QUERY REWRITING
		if len(fullHistory) > 1 {
			log.Printf("[RAG] Rewriting query using context...")
			rw, err := p.rewriteQuery(fullHistory)
			if err == nil && rw != "" {
				refinedQuery = rw
				textToEmbed = rw // Default: embeddiamo la query riscritta
				log.Printf("      ✅ Rewritten in %v: '%s' -> '%s'", time.Since(t1), limitStr(lastQuery, 30), limitStr(refinedQuery, 50))
			} else {
				log.Printf("      ⚠️ Rewrite skipped/failed in %v: %v", time.Since(t1), err)
			}
		}

		// STAGE 2: GROUNDED HYDE
		if p.cfg.RAGUseHyDe && p.llmClient != nil {
			t2 := time.Now()
			log.Printf("[2/4] 🔍 Grounding Search (Pre-search)...")

			// Grounding con la query riscritta
			groundingVec, _ := p.cfg.Embedder.Embed(refinedQuery)

			if groundingVec != nil {
				// Usiamo una ricerca leggera per trovare contesto
				snippets, _ := p.engine.VSearchGraph(p.cfg.RAGIndex, groundingVec, 20, "", "", 100, 0.5, nil, true)

				log.Printf("      Found %d snippets for grounding", len(snippets))

				var snippetText strings.Builder
				for _, s := range snippets {
					content := getTextFromMeta(s.Node.VectorData.Metadata)
					if len(content) > 1000 {
						content = content[:1000] + "..."
					}
					content = strings.ReplaceAll(content, "\n", " ")
					snippetText.WriteString("- " + content + "\n")
				}

				// Generazione Ipotesi solo se abbiamo trovato grounding
				if snippetText.Len() > 0 {
					log.Printf("[3/4] 💭 Generating HyDe Hypothesis...")
					hypo, err := p.generateGroundedHyDe(refinedQuery, snippetText.String())
					if err == nil && hypo != "" {
						textToEmbed = hypo // Ora embedderemo l'ipotesi
						log.Printf("      ✅ Hypothesis generated in %v (%d chars)", time.Since(t2), len(hypo))
						log.Printf("      📄 Preview: %s", limitStr(hypo, 100))
					} else {
						log.Printf("      ❌ HyDe generation failed: %v", err)
					}
				} else {
					log.Printf("      ⚠️ Grounding found no context. HyDe might drift.")
				}
			}
		}
	}

	// STAGE 3: CALCOLO VETTORI (Dual Vector Strategy)
	// Calcoliamo sia il vettore "Originale" (sicuro) che quello "HyDe" (sperimentale)
	t4 := time.Now()
	log.Printf("[4/4] 🔢 Embedding Strategy...")

	var originalVec []float32
	var hydeVec []float32

	// 1. Calcolo Vettore Originale (sempre utile come fallback o firewall)
	originalVec, _ = p.cfg.Embedder.Embed(refinedQuery)

	// 2. Calcolo Vettore HyDe (solo se è stato generato un testo diverso dalla query)
	if textToEmbed != "" && textToEmbed != refinedQuery {
		v, err := p.cfg.Embedder.Embed(textToEmbed)
		if err == nil {
			hydeVec = v
			log.Printf("      ✅ HyDe Vector computed")
		}
	}
	log.Printf("      ✅ Embedding phase completed in %v", time.Since(t4))

	// FIREWALL CHECK (Usiamo originalVec per sicurezza)
	if p.cfg.FirewallEnabled && len(originalVec) > 0 {
		if blocked, reason := p.checkFirewallWithVec(originalVec); blocked {
			log.Printf("[Firewall] ⛔ BLOCKED: %s", reason)
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("Blocked by Semantic Firewall: %s", reason),
			})
			return
		}
	}

	// STAGE 4: RAG INJECTION CON FALLBACK
	if p.cfg.RAGEnabled && strings.Contains(r.URL.Path, "/chat/completions") {
		tRag := time.Now()
		log.Printf("[RAG] 🚀 Injecting Context...")

		var finalBody []byte
		var errInjection error
		usedStrategy := "Standard"

		// TENTATIVO 1: Usa HyDe (se disponibile)
		if len(hydeVec) > 0 {
			log.Printf("      Attempt 1: Using HyDe Vector...")
			finalBody, errInjection = p.performRAGInjection(bodyBytes, hydeVec, refinedQuery)
			if finalBody != nil {
				usedStrategy = "HyDe"
			}
		}

		// TENTATIVO 2: Fallback su Originale (Safety Net)
		// Se HyDe non c'era OPPURE ha fallito (nil body), usa originale
		if finalBody == nil && len(originalVec) > 0 {
			if len(hydeVec) > 0 {
				log.Printf("      ⚠️ HyDe yielded no results. Fallback to Original Vector.")
			} else {
				log.Printf("      Attempt 1: Using Standard Search (HyDe skipped).")
			}
			finalBody, errInjection = p.performRAGInjection(bodyBytes, originalVec, refinedQuery)
			usedStrategy = "Fallback/Standard"
		}

		if errInjection == nil && finalBody != nil {
			bodyBytes = finalBody
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			r.ContentLength = int64(len(bodyBytes))
			r.Header.Set("Content-Length", strconv.Itoa(len(bodyBytes)))
			log.Printf("      ✅ Context Injected (%s) in %v. Forwarding...", usedStrategy, time.Since(tRag))
		} else {
			log.Printf("      ❌ CRITICAL: No context found even after fallback. LLM will answer blindly.")
		}
	}

	// 4. CACHE READ
	// Usiamo originalVec per la cache per massimizzare le hit su domande simili
	if !isStreaming && p.cfg.CacheEnabled && len(originalVec) > 0 {
		if cachedResp, hit := p.checkCache(originalVec); hit {
			log.Printf("[Cache] HIT")
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

	log.Printf("=== ✅ REQUEST COMPLETED in %v ===\n", time.Since(totalStart))

	if capturer.statusCode == http.StatusOK {
		go p.saveToCache(originalVec, lastQuery, capturer.body.Bytes())
	}
}

// --- HELPER FUNCTIONS ---

// FIX: Funzione mancante nel tuo snippet precedente
func isSystemTask(text string) bool {
	// Pattern comuni di Open WebUI per task automatici
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
					log.Printf("[Cache] Item expired. Cleanup.")
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

func (p *AIProxy) saveToCache(queryVec []float32, queryText string, responseBytes []byte) {
	if len(responseBytes) == 0 {
		return
	}
	info, err := p.engine.DB.GetSingleVectorIndexInfoAPI(p.cfg.CacheIndex)
	if err == nil {
		if p.cfg.MaxCacheItems > 0 && info.VectorCount >= p.cfg.MaxCacheItems {
			return
		}
	}
	id := fmt.Sprintf("cache_%d_%d", time.Now().UnixNano(), len(queryText))
	meta := map[string]interface{}{
		"query":      queryText,
		"response":   string(responseBytes),
		"created_at": float64(time.Now().Unix()),
	}
	maintConfig := &hnsw.AutoMaintenanceConfig{
		VacuumInterval:  hnsw.Duration(p.cfg.CacheVacuumInterval),
		DeleteThreshold: p.cfg.CacheDeleteThreshold,
		RefineEnabled:   false,
	}
	_ = p.engine.VCreate(p.cfg.CacheIndex, distance.Cosine, 16, 200, distance.Float32, "", maintConfig)
	p.engine.VAdd(p.cfg.CacheIndex, id, queryVec, meta)
	log.Printf("[Cache] Saved.")
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

func (p *AIProxy) performRAGInjection(originalBody []byte, queryVec []float32, queryText string) ([]byte, error) {
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

	results, err := p.engine.VSearchGraph(p.cfg.RAGIndex, queryVec, p.cfg.RAGTopK, filter, hybridQuery, efSearch, alpha, relations, true)
	if err != nil || len(results) == 0 {
		return nil, nil
	}

	var contextBuilder strings.Builder
	foundRelevant := false
	seenContent := make(map[string]struct{})

	for _, res := range results {
		if float32(res.Score) < p.cfg.RAGThreshold {
			continue
		}

		mainText := getTextFromMeta(res.Node.VectorData.Metadata)
		if mainText == "" {
			continue
		}

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

		blockHash := sourceName + mainText
		if _, exists := seenContent[blockHash]; exists {
			continue
		}
		seenContent[blockHash] = struct{}{}

		contextBuilder.WriteString(fmt.Sprintf("--- Document: %s ---\n", sourceName))
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
	}

	if !foundRelevant {
		return nil, nil
	}

	promptTemplate := p.cfg.RAGSystemPrompt
	if promptTemplate == "" {
		promptTemplate = "Context:\n{{context}}\nQuestion:\n{{query}}"
	}

	finalContent := strings.ReplaceAll(promptTemplate, "{{context}}", contextBuilder.String())
	finalContent = strings.ReplaceAll(finalContent, "{{query}}", queryText)

	var requestData map[string]interface{}
	if err := json.Unmarshal(originalBody, &requestData); err != nil {
		return nil, err
	}
	messages, ok := requestData["messages"].([]interface{})
	if !ok || len(messages) == 0 {
		return nil, nil
	}
	lastMsgIdx := len(messages) - 1
	lastMsg, ok := messages[lastMsgIdx].(map[string]interface{})
	if !ok {
		return nil, nil
	}
	if _, ok := lastMsg["content"].(string); ok {
		lastMsg["content"] = finalContent
		messages[lastMsgIdx] = lastMsg
		requestData["messages"] = messages
	} else {
		return nil, nil
	}

	return json.Marshal(requestData)
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
