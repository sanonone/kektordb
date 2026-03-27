package cognitive

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

// --- MOCK LLM ---
type MockLLM struct {
	Called             bool
	ReceivedPrompt     string
	ResponseText       string
	ContradictionReply string // Returned when system prompt contains "contradict"
	PreferenceReply    string // Returned when system prompt contains "user behavior"
	FailureReply       string // Returned when system prompt contains "debugging"
	EvolutionReply     string // Returned when system prompt contains "evolution"
}

func (m *MockLLM) Chat(systemPrompt, userQuery string) (string, error) {
	m.Called = true
	m.ReceivedPrompt = userQuery
	if strings.Contains(systemPrompt, "contradict") && m.ContradictionReply != "" {
		return m.ContradictionReply, nil
	}
	if strings.Contains(systemPrompt, "user behavior") && m.PreferenceReply != "" {
		return m.PreferenceReply, nil
	}
	if strings.Contains(systemPrompt, "debugging") && m.FailureReply != "" {
		return m.FailureReply, nil
	}
	if strings.Contains(systemPrompt, "evolution") && m.EvolutionReply != "" {
		return m.EvolutionReply, nil
	}
	return m.ResponseText, nil
}

func (m *MockLLM) ChatWithImages(systemPrompt, userQuery string, images [][]byte) (string, error) {
	return "", nil
}

// --- TEST SUITE ---
func TestCognitiveMemoryConsolidation(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "mcp_memory"
	// FIX: Usiamo Cosine per simulare un vero text embedding
	err = eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// FIX: Usiamo un vettore già normalizzato per il Coseno [1.0, 0.0]
	for i := 0; i < 5; i++ {
		id := "mem_clone_" + string(rune('A'+i))
		err := eng.VAdd(indexName, id, []float32{1.0, 0.0}, map[string]any{
			"content": "Python is a good language but a bit slow",
			"type":    "working_memory",
		})
		if err != nil {
			t.Fatalf("Failed to add vector: %v", err)
		}
	}

	// Outlier: Vettore ortogonale [0.0, 1.0]
	eng.VAdd(indexName, "mem_outlier", []float32{0.0, 1.0}, map[string]any{
		"content": "I really love eating pizza in Rome",
		"type":    "working_memory",
	})

	mockBrain := &MockLLM{
		ResponseText: "Python is a popular programming language, known for being good but somewhat slow.",
	}
	cfg := Config{Enabled: true, Interval: 1 * time.Hour}
	gardener := NewGardener(eng, mockBrain, cfg)

	// --- FASE 1: TEST CLUSTERING ---
	t.Log("Testing Clustering Algorithm...")
	clusters := gardener.findRedundantClusters(indexName, 0.90, 4)

	if len(clusters) != 1 {
		t.Fatalf("Expected exactly 1 cluster, found %d", len(clusters))
	}

	cluster := clusters[0]
	if len(cluster.MemberIDs) != 5 {
		t.Errorf("Expected cluster to have 5 members, got %d", len(cluster.MemberIDs))
	}
	if slices.Contains(cluster.MemberIDs, "mem_outlier") {
		t.Errorf("Cluster accidentally included the outlier memory!")
	}

	// --- FASE 2: TEST SINTESI LLM ---
	t.Log("Testing LLM Synthesis & Graph Linking...")
	gardener.consolidateCluster(indexName, cluster)

	if !mockBrain.Called {
		t.Errorf("LLM was never called during consolidation!")
	}

	// --- FASE 3: VERIFICA TOPOLOGIA GRAFO E METADATI ---
	t.Log("Verifying Graph and Metadata integrity...")

	oldMem, err := eng.VGet(indexName, "mem_clone_A")
	if err != nil {
		t.Fatalf("Failed to fetch old memory: %v", err)
	}

	if archived, ok := oldMem.Metadata["_archived"].(bool); !ok || !archived {
		t.Errorf("Old memory was not marked as _archived=true")
	}

	outLinks, found := eng.VGetLinks(indexName, "mem_clone_A", "consolidated_into")
	if !found || len(outLinks) != 1 {
		t.Fatalf("Old memory missing 'consolidated_into' graph link to Master Memory")
	}

	masterID := outLinks[0]
	masterMem, err := eng.VGet(indexName, masterID)
	if err != nil {
		t.Fatalf("Master Memory node not found in index: %v", err)
	}

	if masterMem.Metadata["content"] != mockBrain.ResponseText {
		t.Errorf("Master Memory content mismatch. Expected '%s', got '%v'", mockBrain.ResponseText, masterMem.Metadata["content"])
	}

	if masterMem.Metadata["type"] != "consolidated_memory" {
		t.Errorf("Master Memory type mismatch")
	}

	// FIX: Chiediamo quali link USCENTI ha il Master Node di tipo "derived_from"
	outLinksDerived, found := eng.VGetLinks(indexName, masterID, "derived_from")
	if !found || len(outLinksDerived) != 5 {
		t.Errorf("Master Memory missing 'derived_from' outgoing links to original memories. Expected 5, got %d", len(outLinksDerived))
	}

	// FIX: Check vettoriale calibrato sul Coseno (media di[1.0, 0.0] è [1.0, 0.0])
	if masterMem.Vector[0] < 0.99 || masterMem.Vector[0] > 1.01 {
		t.Errorf("Master Memory Vector Average failed. Expected ~1.0, got %f", masterMem.Vector[0])
	}

	t.Log("✅ Cognitive Consolidation Test Passed Successfully!")
}

func TestContradictionDetection(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "mcp_memory"
	err = eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// Due memorie semanticamente vicine (dopo L2-norm + cosine score ~0.94)
	// ma che affermano cose opposte. Range 0.70-0.95 per contradiction detection.
	eng.VAdd(indexName, "mem_fast", []float32{0.5, 0.75, 0.1}, map[string]any{
		"content": "Python is a fast language",
		"type":    "working_memory",
	})
	eng.VAdd(indexName, "mem_slow", []float32{0.7, 0.3, 0.2}, map[string]any{
		"content": "Python is a slow language",
		"type":    "working_memory",
	})

	// Outlier ortogonale: score ~0.57 con mem_slow (filtrato da <0.70)
	eng.VAdd(indexName, "mem_pizza", []float32{0.0, 0.0, 1.0}, map[string]any{
		"content": "I love eating pizza in Rome",
		"type":    "working_memory",
	})

	mockBrain := &MockLLM{
		ContradictionReply: `{"contradiction": true, "reason": "One states Python is fast, the other states it is slow", "suggested_resolution": "Verify benchmark results and keep the most recent memory.", "action_required": true}`,
	}
	cfg := Config{Enabled: true, Interval: 1 * time.Hour}
	gardener := NewGardener(eng, mockBrain, cfg)

	gardener.detectContradictions(indexName)

	if !mockBrain.Called {
		t.Errorf("LLM was never called during contradiction detection")
	}

	// Conta le reflection create
	ids, _, _ := eng.VGetIDsByCursor(indexName, 0, 100)
	var reflectionIDs []string
	for _, id := range ids {
		if strings.HasPrefix(id, "reflection_") {
			reflectionIDs = append(reflectionIDs, id)
		}
	}

	if len(reflectionIDs) != 1 {
		t.Fatalf("Expected exactly 1 reflection, found %d", len(reflectionIDs))
	}

	reflectionID := reflectionIDs[0]
	reflection, err := eng.VGet(indexName, reflectionID)
	if err != nil {
		t.Fatalf("Reflection node not found: %v", err)
	}

	// Verifica metadati reflection
	if reflection.Metadata["type"] != "reflection" {
		t.Errorf("Expected type=reflection, got %v", reflection.Metadata["type"])
	}
	if reflection.Metadata["status"] != "unresolved" {
		t.Errorf("Expected status=unresolved, got %v", reflection.Metadata["status"])
	}
	content, _ := reflection.Metadata["content"].(string)
	if !strings.Contains(content, "One states Python is fast") {
		t.Errorf("Reflection content mismatch: %s", content)
	}

	// Verifica suggested_resolution e action_required
	sr, _ := reflection.Metadata["suggested_resolution"].(string)
	if sr != "Verify benchmark results and keep the most recent memory." {
		t.Errorf("Expected suggested_resolution from LLM, got: %s", sr)
	}
	ar, _ := reflection.Metadata["action_required"].(bool)
	if !ar {
		t.Errorf("Expected action_required=true, got false")
	}

	// Verifica archi contradicts dalla reflection verso le memorie
	links, found := eng.VGetLinks(indexName, reflectionID, "contradicts")
	if !found || len(links) != 2 {
		t.Fatalf("Expected 2 'contradicts' links from reflection, got %d", len(links))
	}
	if !slices.Contains(links, "mem_fast") || !slices.Contains(links, "mem_slow") {
		t.Errorf("Reflection not linked to both conflicting memories. Links: %v", links)
	}

	// Verifica che mem_pizza NON sia coinvolto (score troppo basso)
	pizzaLinks, _ := eng.VGetLinks(indexName, "mem_pizza", "contradicted_by")
	if len(pizzaLinks) > 0 {
		t.Errorf("mem_pizza was incorrectly linked to a contradiction")
	}

	// Verifica archi analyzed_against (deduplicazione)
	analyzedLinks, found := eng.VGetLinks(indexName, "mem_fast", "analyzed_against")
	if !found || !slices.Contains(analyzedLinks, "mem_slow") {
		t.Errorf("Missing analyzed_against link between mem_fast and mem_slow")
	}

	t.Log("✅ Contradiction Detection Test Passed Successfully!")
}

func TestImportanceShiftDetection(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "mcp_memory"
	err = eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// Entità con 10 incoming "mentions" (tutti creati "ora" = tutti recenti)
	eng.VAdd(indexName, "topic_AI", []float32{1.0, 0.0}, map[string]any{
		"content": "Artificial Intelligence",
		"type":    "entity",
	})
	for i := 0; i < 10; i++ {
		memID := "mem_mentions_" + string(rune('A'+i))
		eng.VAdd(indexName, memID, []float32{0.5, 0.5}, map[string]any{
			"content": "memory about AI topic",
		})
		eng.VLink(indexName, memID, "topic_AI", "mentions", "mentioned_in", 1.0, nil)
	}

	// Entità con 4 mentions (< 5, non deve triggerare shift)
	eng.VAdd(indexName, "topic_Low", []float32{0.0, 1.0}, map[string]any{
		"content": "Low Interest Topic",
		"type":    "entity",
	})
	for i := 0; i < 4; i++ {
		memID := "mem_low_" + string(rune('A'+i))
		eng.VAdd(indexName, memID, []float32{0.5, 0.5}, map[string]any{
			"content": "memory about low topic",
		})
		eng.VLink(indexName, memID, "topic_Low", "mentions", "mentioned_in", 1.0, nil)
	}

	mockBrain := &MockLLM{}
	cfg := Config{Enabled: true, Interval: 1 * time.Hour}
	gardener := NewGardener(eng, mockBrain, cfg)

	// Prima chiamata: deve creare la reflection
	gardener.detectImportanceShifts(indexName)

	ids, _, _ := eng.VGetIDsByCursor(indexName, 0, 100)
	var reflectionIDs []string
	for _, id := range ids {
		if strings.HasPrefix(id, "reflection_") {
			reflectionIDs = append(reflectionIDs, id)
		}
	}

	// Solo topic_AI deve triggerare (10 mentions > 5), topic_Low no (4 < 5)
	if len(reflectionIDs) != 1 {
		t.Fatalf("Expected exactly 1 reflection for topic_AI, found %d", len(reflectionIDs))
	}

	reflectionID := reflectionIDs[0]
	reflection, err := eng.VGet(indexName, reflectionID)
	if err != nil {
		t.Fatalf("Reflection node not found: %v", err)
	}

	// Verifica metadati
	if reflection.Metadata["type"] != "reflection" {
		t.Errorf("Expected type=reflection, got %v", reflection.Metadata["type"])
	}
	if reflection.Metadata["status"] != "insight" {
		t.Errorf("Expected status=insight, got %v", reflection.Metadata["status"])
	}
	conf, _ := reflection.Metadata["confidence"].(float64)
	if conf < 0.5 || conf > 1.0 {
		t.Errorf("Expected confidence between 0.5-1.0 (10 mentions), got %v", conf)
	}
	content, _ := reflection.Metadata["content"].(string)
	if !strings.Contains(content, "topic_AI") {
		t.Errorf("Reflection content should reference topic_AI: %s", content)
	}
	if !strings.Contains(content, "10 new mentions") {
		t.Errorf("Reflection content should mention the spike count: %s", content)
	}

	// Verifica arco focus_shifted dalla reflection verso topic_AI
	shiftLinks, found := eng.VGetLinks(indexName, reflectionID, "focus_shifted")
	if !found || len(shiftLinks) != 1 || shiftLinks[0] != "topic_AI" {
		t.Errorf("Missing or incorrect focus_shifted link. Got: %v", shiftLinks)
	}

	// Verifica che topic_Low NON abbia ricevuto una reflection
	lowLinks, _ := eng.VGetIncoming(indexName, "topic_Low", "focus_shifted")
	if len(lowLinks) > 0 {
		t.Errorf("topic_Low should not have a focus_shifted link (only 4 mentions)")
	}

	// Seconda chiamata: anti-spam, non deve creare duplicati
	gardener.detectImportanceShifts(indexName)

	ids2, _, _ := eng.VGetIDsByCursor(indexName, 0, 100)
	var allReflectionIDs []string
	for _, id := range ids2 {
		if strings.HasPrefix(id, "reflection_") {
			allReflectionIDs = append(allReflectionIDs, id)
		}
	}

	if len(allReflectionIDs) != 1 {
		t.Errorf("Anti-spam failed: expected still 1 reflection, found %d", len(allReflectionIDs))
	}

	t.Log("✅ Importance Shift Detection Test Passed Successfully!")
}

// addOldEdge creates a graph edge with a custom timestamp directly in the core DB.
// This bypasses VLink (which always uses time.Now()) for testing time-travel features.
func addOldEdge(eng *engine.Engine, indexName, sourceID, targetID, relType string, ts int64) {
	src := fmt.Sprintf("%s::%s", indexName, sourceID)
	tgt := fmt.Sprintf("%s::%s", indexName, targetID)
	eng.DB.AddEdge(src, tgt, relType, 1.0, nil, ts)
	eng.DB.AddEdge(tgt, src, relType, 1.0, nil, ts) // bidirectional for GetIncomingEdges
}

// countReflections scans all IDs in the index and returns those prefixed with "reflection_".
func countReflections(eng *engine.Engine, indexName string) []string {
	ids, _, _ := eng.VGetIDsByCursor(indexName, 0, 500)
	var out []string
	for _, id := range ids {
		if strings.HasPrefix(id, "reflection_") {
			out = append(out, id)
		}
	}
	return out
}

func TestKnowledgeGapsDetection(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "mcp_memory"
	err = eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// Entity_A e Entity_B: vicini semantici (score ~0.94) SENZA arco → GAP
	eng.VAdd(indexName, "entity_A", []float32{0.5, 0.75, 0.1}, map[string]any{
		"name": "Python", "type": "entity",
	})
	eng.VAdd(indexName, "entity_B", []float32{0.7, 0.3, 0.2}, map[string]any{
		"name": "Django", "type": "entity",
	})

	// Entity_C: vicino semantico di Entity_A CON arco esplicito → NO GAP
	eng.VAdd(indexName, "entity_C", []float32{0.55, 0.7, 0.15}, map[string]any{
		"name": "Flask", "type": "entity",
	})
	eng.VLink(indexName, "entity_A", "entity_C", "related_to", "related_to", 1.0, nil)
	// Link also B↔C to prevent B↔C from being detected as a gap
	eng.VLink(indexName, "entity_B", "entity_C", "related_to", "related_to", 1.0, nil)

	// Mem_D: working_memory (non-entity), vicina semantica → FILTRATA
	eng.VAdd(indexName, "mem_D", []float32{0.5, 0.75, 0.1}, map[string]any{
		"content": "Python is great", "type": "working_memory",
	})

	gardener := NewGardener(eng, &MockLLM{}, Config{Enabled: true, Interval: time.Hour})
	gardener.detectKnowledgeGaps(indexName)

	refs := countReflections(eng, indexName)
	if len(refs) != 1 {
		t.Fatalf("Expected 1 reflection (only entity_A↔entity_B gap), found %d", len(refs))
	}

	ref, _ := eng.VGet(indexName, refs[0])
	if ref.Metadata["status"] != "insight" {
		t.Errorf("Expected status=insight, got %v", ref.Metadata["status"])
	}
	if conf, ok := ref.Metadata["confidence"].(float64); !ok || conf <= 0 {
		t.Errorf("Expected confidence > 0, got %v", ref.Metadata["confidence"])
	}
	content, _ := ref.Metadata["content"].(string)
	if !strings.Contains(content, "Python") || !strings.Contains(content, "Django") {
		t.Errorf("Reflection should reference both entity names: %s", content)
	}

	// Verifica suggests_link (unidirezionale, no inverse)
	suggestLinks, found := eng.VGetLinks(indexName, refs[0], "suggests_link")
	if !found || len(suggestLinks) != 2 {
		t.Fatalf("Expected 2 suggests_link from reflection, got %d", len(suggestLinks))
	}

	// Verifica gap_analyzed dedup
	gapLinks, found := eng.VGetLinks(indexName, "entity_A", "gap_analyzed")
	if !found || !slices.Contains(gapLinks, "entity_B") {
		t.Errorf("Missing gap_analyzed link between entity_A and entity_B")
	}

	t.Log("✅ Knowledge Gaps Detection Test Passed Successfully!")
}

func TestKnowledgeGapsDeduplication(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "mcp_memory"
	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)

	eng.VAdd(indexName, "ent_X", []float32{0.5, 0.75, 0.1}, map[string]any{"name": "Alpha", "type": "entity"})
	eng.VAdd(indexName, "ent_Y", []float32{0.7, 0.3, 0.2}, map[string]any{"name": "Beta", "type": "entity"})

	gardener := NewGardener(eng, &MockLLM{}, Config{Enabled: true, Interval: time.Hour})

	// Prima chiamata: crea reflection
	gardener.detectKnowledgeGaps(indexName)
	// Seconda chiamata: dedup, non deve creare duplicati
	gardener.detectKnowledgeGaps(indexName)

	refs := countReflections(eng, indexName)
	if len(refs) != 1 {
		t.Errorf("Dedup failed: expected 1 reflection after 2 calls, found %d", len(refs))
	}

	t.Log("✅ Knowledge Gaps Deduplication Test Passed Successfully!")
}

func TestSentimentShiftDetection(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "mcp_memory"
	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)

	now := time.Now().UnixNano()
	twoWeeksAgo := now - (14 * 24 * int64(time.Hour))
	oldTS := twoWeeksAgo - int64(time.Hour) // > 14 giorni fa

	// Entity con mentions: 2 vecchie negative + 2 recenti positive → shift positivo
	eng.VAdd(indexName, "entity_sent", []float32{1.0, 0.0}, map[string]any{"name": "KektorDB", "type": "entity"})

	// Vecchie mentions (negative sentiment)
	eng.VAdd(indexName, "old_neg_1", []float32{0.5, 0.5}, map[string]any{"content": "This is terrible and frustrating"})
	eng.VAdd(indexName, "old_neg_2", []float32{0.5, 0.5}, map[string]any{"content": "Really bad and disappointing experience"})
	addOldEdge(eng, indexName, "old_neg_1", "entity_sent", "mentions", oldTS)
	addOldEdge(eng, indexName, "old_neg_2", "entity_sent", "mentions", oldTS)

	// Recenti mentions (positive sentiment)
	eng.VAdd(indexName, "new_pos_1", []float32{0.5, 0.5}, map[string]any{"content": "This is great and excellent"})
	eng.VAdd(indexName, "new_pos_2", []float32{0.5, 0.5}, map[string]any{"content": "Amazing work, I love it"})
	eng.VLink(indexName, "new_pos_1", "entity_sent", "mentions", "mentioned_in", 1.0, nil)
	eng.VLink(indexName, "new_pos_2", "entity_sent", "mentions", "mentioned_in", 1.0, nil)

	gardener := NewGardener(eng, &MockLLM{}, Config{Enabled: true, Interval: time.Hour})
	gardener.detectSentimentShifts(indexName, "english")

	refs := countReflections(eng, indexName)
	if len(refs) != 1 {
		t.Fatalf("Expected 1 sentiment reflection, found %d", len(refs))
	}

	ref, _ := eng.VGet(indexName, refs[0])
	if ref.Metadata["status"] != "insight" {
		t.Errorf("Expected status=insight, got %v", ref.Metadata["status"])
	}
	content, _ := ref.Metadata["content"].(string)
	if !strings.Contains(content, "positive") {
		t.Errorf("Expected positive direction in reflection: %s", content)
	}

	// Verifica archi sentiment_shift
	shiftLinks, found := eng.VGetLinks(indexName, refs[0], "sentiment_shift")
	if !found || len(shiftLinks) != 1 || shiftLinks[0] != "entity_sent" {
		t.Errorf("Missing sentiment_shift link to entity_sent")
	}

	// Verifica self-link dedup
	selfLinks, found := eng.VGetLinks(indexName, "entity_sent", "sentiment_analyzed")
	if !found || len(selfLinks) == 0 {
		t.Errorf("Missing sentiment_analyzed self-link for dedup")
	}

	// Seconda chiamata: anti-spam
	gardener.detectSentimentShifts(indexName, "english")
	refs2 := countReflections(eng, indexName)
	if len(refs2) != 1 {
		t.Errorf("Anti-spam failed: expected 1 reflection, found %d", len(refs2))
	}

	t.Log("✅ Sentiment Shift Detection Test Passed Successfully!")
}

func TestCentralityShiftDetection(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "mcp_memory"
	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)

	now := time.Now().UnixNano()
	oneMonthAgo := now - (30 * 24 * int64(time.Hour))
	oldTS := oneMonthAgo - int64(time.Hour) // > 30 giorni fa

	eng.VAdd(indexName, "hub_node", []float32{1.0, 0.0}, map[string]any{"name": "AI", "type": "entity"})

	// 3 archi vecchi (> 30gg) → degreePast = 3
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("old_link_%c", rune('A'+i))
		eng.VAdd(indexName, id, []float32{0.5, 0.5}, map[string]any{"content": "old memory"})
		addOldEdge(eng, indexName, id, "hub_node", "related_to", oldTS)
	}

	// 12 archi recenti (via VLink) → degreeNow = 15
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("new_link_%c", rune('A'+i))
		eng.VAdd(indexName, id, []float32{0.5, 0.5}, map[string]any{"content": "new memory"})
		eng.VLink(indexName, id, "hub_node", "related_to", "related_to", 1.0, nil)
	}

	gardener := NewGardener(eng, &MockLLM{}, Config{Enabled: true, Interval: time.Hour})
	gardener.detectCentralityShifts(indexName)

	refs := countReflections(eng, indexName)
	if len(refs) != 1 {
		t.Fatalf("Expected 1 centrality reflection, found %d", len(refs))
	}

	ref, _ := eng.VGet(indexName, refs[0])
	if ref.Metadata["status"] != "insight" {
		t.Errorf("Expected status=insight, got %v", ref.Metadata["status"])
	}
	content, _ := ref.Metadata["content"].(string)
	if !strings.Contains(content, "hub_node") {
		t.Errorf("Reflection should reference hub_node: %s", content)
	}

	// Verifica archi became_central
	centralLinks, found := eng.VGetLinks(indexName, refs[0], "became_central")
	if !found || len(centralLinks) != 1 || centralLinks[0] != "hub_node" {
		t.Errorf("Missing became_central link to hub_node")
	}

	// Anti-spam: seconda chiamata
	gardener.detectCentralityShifts(indexName)
	refs2 := countReflections(eng, indexName)
	if len(refs2) != 1 {
		t.Errorf("Anti-spam failed: expected 1 reflection, found %d", len(refs2))
	}

	t.Log("✅ Centrality Shift Detection Test Passed Successfully!")
}

func TestForgettingPatternsDetection(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "mcp_memory"
	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)

	now := time.Now().UnixNano()
	oneMonthAgo := now - (30 * 24 * int64(time.Hour))
	oldTS := oneMonthAgo - int64(time.Hour) // > 30 giorni fa

	// Entità abbandonata: 5 mentions tutte vecchie (> 30gg) → decay
	eng.VAdd(indexName, "forgotten", []float32{1.0, 0.0}, map[string]any{"name": "LegacyAPI", "type": "entity"})
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("old_mention_%c", rune('A'+i))
		eng.VAdd(indexName, id, []float32{0.5, 0.5}, map[string]any{"content": "old memory"})
		addOldEdge(eng, indexName, id, "forgotten", "mentions", oldTS)
	}

	// Entità attiva: 5 mentions recenti → NO decay
	eng.VAdd(indexName, "active", []float32{0.0, 1.0}, map[string]any{"name": "NewFeature", "type": "entity"})
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("new_mention_%c", rune('A'+i))
		eng.VAdd(indexName, id, []float32{0.5, 0.5}, map[string]any{"content": "recent memory"})
		eng.VLink(indexName, id, "active", "mentions", "mentioned_in", 1.0, nil)
	}

	gardener := NewGardener(eng, &MockLLM{}, Config{Enabled: true, Interval: time.Hour})
	gardener.detectForgettingPatterns(indexName)

	refs := countReflections(eng, indexName)
	if len(refs) != 1 {
		t.Fatalf("Expected 1 decay reflection (only forgotten), found %d", len(refs))
	}

	ref, _ := eng.VGet(indexName, refs[0])
	if conf, ok := ref.Metadata["confidence"].(float64); !ok || conf <= 0 {
		t.Errorf("Expected confidence > 0, got %v", ref.Metadata["confidence"])
	}
	content, _ := ref.Metadata["content"].(string)
	if !strings.Contains(content, "forgotten") {
		t.Errorf("Reflection should reference forgotten entity: %s", content)
	}

	// Verifica archi knowledge_decay
	decayLinks, found := eng.VGetLinks(indexName, refs[0], "knowledge_decay")
	if !found || len(decayLinks) != 1 || decayLinks[0] != "forgotten" {
		t.Errorf("Missing knowledge_decay link to forgotten entity")
	}

	// Anti-spam: seconda chiamata
	gardener.detectForgettingPatterns(indexName)
	refs2 := countReflections(eng, indexName)
	if len(refs2) != 1 {
		t.Errorf("Anti-spam failed: expected 1 reflection, found %d", len(refs2))
	}

	t.Log("✅ Forgetting Patterns Detection Test Passed Successfully!")
}

func TestSentimentShiftItalian(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "mcp_memory"
	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "italian", nil, nil, nil)

	now := time.Now().UnixNano()
	twoWeeksAgo := now - (14 * 24 * int64(time.Hour))
	oldTS := twoWeeksAgo - int64(time.Hour)

	eng.VAdd(indexName, "entity_ita", []float32{1.0, 0.0}, map[string]any{"name": "KektorDB", "type": "entity"})

	// Old mentions (negative Italian sentiment)
	eng.VAdd(indexName, "old_neg_1", []float32{0.5, 0.5}, map[string]any{"content": "Servizio terribile e frustrante"})
	eng.VAdd(indexName, "old_neg_2", []float32{0.5, 0.5}, map[string]any{"content": "Davvero brutto e deludente"})
	addOldEdge(eng, indexName, "old_neg_1", "entity_ita", "mentions", oldTS)
	addOldEdge(eng, indexName, "old_neg_2", "entity_ita", "mentions", oldTS)

	// Recent mentions (positive Italian sentiment)
	eng.VAdd(indexName, "new_pos_1", []float32{0.5, 0.5}, map[string]any{"content": "Questo framework è fantastico e ottimo"})
	eng.VAdd(indexName, "new_pos_2", []float32{0.5, 0.5}, map[string]any{"content": "Eccellente lavoro, molto piacevole"})
	eng.VLink(indexName, "new_pos_1", "entity_ita", "mentions", "mentioned_in", 1.0, nil)
	eng.VLink(indexName, "new_pos_2", "entity_ita", "mentions", "mentioned_in", 1.0, nil)

	gardener := NewGardener(eng, &MockLLM{}, Config{Enabled: true, Interval: time.Hour})
	gardener.detectSentimentShifts(indexName, "italian")

	refs := countReflections(eng, indexName)
	if len(refs) != 1 {
		t.Fatalf("Expected 1 sentiment reflection for Italian, found %d", len(refs))
	}

	ref, _ := eng.VGet(indexName, refs[0])
	content, _ := ref.Metadata["content"].(string)
	if !strings.Contains(content, "positive") {
		t.Errorf("Expected positive direction in reflection: %s", content)
	}

	t.Log("✅ Sentiment Shift Italian Test Passed Successfully!")
}

func TestSentimentShiftUnsupportedLanguage(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "mcp_memory"
	// Empty language — no sentiment lexicon available.
	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "", nil, nil, nil)

	eng.VAdd(indexName, "entity_unsup", []float32{1.0, 0.0}, map[string]any{"type": "entity"})
	eng.VAdd(indexName, "mem_pos", []float32{0.5, 0.5}, map[string]any{"content": "This is great and amazing"})
	eng.VLink(indexName, "mem_pos", "entity_unsup", "mentions", "mentioned_in", 1.0, nil)

	gardener := NewGardener(eng, &MockLLM{}, Config{Enabled: true, Interval: time.Hour})
	// Should silently return without creating any reflection.
	gardener.detectSentimentShifts(indexName, "")

	refs := countReflections(eng, indexName)
	if len(refs) != 0 {
		t.Errorf("Expected 0 reflections for unsupported language, found %d", len(refs))
	}

	// Also test with a completely unknown language.
	gardener.detectSentimentShifts(indexName, "spanish")
	refs2 := countReflections(eng, indexName)
	if len(refs2) != 0 {
		t.Errorf("Expected 0 reflections for 'spanish', found %d", len(refs2))
	}

	t.Log("✅ Sentiment Shift Unsupported Language Test Passed Successfully!")
}

func TestConsolidationEdgeTransfer(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "mcp_memory"
	err = eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// 5 clone memories on Python (same vector → score ~1.0)
	for i := 0; i < 5; i++ {
		id := "mem_clone_" + string(rune('A'+i))
		eng.VAdd(indexName, id, []float32{1.0, 0.0}, map[string]any{
			"content": "Python is a good language but a bit slow",
			"type":    "working_memory",
		})
	}

	// External entities
	eng.VAdd(indexName, "entity_Python", []float32{0.5, 0.5}, map[string]any{"name": "Python", "type": "entity"})
	eng.VAdd(indexName, "entity_Django", []float32{0.3, 0.7}, map[string]any{"name": "Django", "type": "entity"})
	eng.VAdd(indexName, "entity_AI", []float32{0.3, 0.7}, map[string]any{"name": "AI", "type": "entity"})

	// Outgoing edges from old memories (should transfer to master)
	eng.VLink(indexName, "mem_clone_A", "entity_Python", "mentions", "mentioned_in", 1.0, nil)
	eng.VLink(indexName, "mem_clone_B", "entity_Python", "mentions", "mentioned_in", 1.0, nil) // dedup test
	eng.VLink(indexName, "mem_clone_C", "entity_Django", "related_to", "related_to", 1.0, nil)

	// Incoming edge to an old memory (should redirect to master)
	eng.VLink(indexName, "entity_AI", "mem_clone_D", "references", "referenced_by", 1.0, nil)

	// Intra-cluster edge (should NOT transfer — both ends are in the cluster)
	eng.VLink(indexName, "mem_clone_A", "mem_clone_B", "related_to", "related_to", 1.0, nil)

	// Outlier (not in cluster)
	eng.VAdd(indexName, "mem_outlier", []float32{0.0, 1.0}, map[string]any{
		"content": "I love eating pizza in Rome",
		"type":    "working_memory",
	})

	mockBrain := &MockLLM{
		ResponseText: "Python is a popular language known for being slow but easy to use.",
	}
	gardener := NewGardener(eng, mockBrain, Config{Enabled: true, Interval: 1 * time.Hour})

	// Run consolidation
	clusters := gardener.findRedundantClusters(indexName, 0.90, 5)
	if len(clusters) != 1 {
		t.Fatalf("Expected 1 cluster, found %d", len(clusters))
	}
	gardener.consolidateCluster(indexName, clusters[0])

	// Find master ID via consolidated_into link from mem_clone_A
	masterLinks, found := eng.VGetLinks(indexName, "mem_clone_A", "consolidated_into")
	if !found || len(masterLinks) != 1 {
		t.Fatalf("Failed to find master ID via consolidated_into link")
	}
	masterID := masterLinks[0]

	// 1. Master should have mentions -> entity_Python (dedup: only 1 edge, not 2)
	mentionsLinks, found := eng.VGetLinks(indexName, masterID, "mentions")
	if !found || len(mentionsLinks) != 1 {
		t.Errorf("Expected 1 'mentions' link from master (dedup), got %d", len(mentionsLinks))
	}
	if found && len(mentionsLinks) > 0 && mentionsLinks[0] != "entity_Python" {
		t.Errorf("Expected master -> entity_Python via mentions, got %s", mentionsLinks[0])
	}

	// 2. Master should have related_to -> entity_Django
	relatedLinks, found := eng.VGetLinks(indexName, masterID, "related_to")
	if !found || len(relatedLinks) != 1 {
		t.Errorf("Expected 1 'related_to' link from master, got %d", len(relatedLinks))
	}
	if found && len(relatedLinks) > 0 && relatedLinks[0] != "entity_Django" {
		t.Errorf("Expected master -> entity_Django via related_to, got %s", relatedLinks[0])
	}

	// 3. Master should have incoming references from entity_AI (reverse index works)
	incomingRefs, found := eng.VGetIncoming(indexName, masterID, "references")
	if !found || len(incomingRefs) != 1 {
		t.Errorf("Expected 1 incoming 'references' to master, got %d", len(incomingRefs))
	}
	if found && len(incomingRefs) > 0 && incomingRefs[0] != "entity_AI" {
		t.Errorf("Expected entity_AI -> master via references, got %s", incomingRefs[0])
	}

	// 4. Master should NOT have intra-cluster edges (mem_clone_A -> mem_clone_B was skipped)
	clusterRelated, _ := eng.VGetLinks(indexName, masterID, "related_to")
	for _, target := range clusterRelated {
		if target == "mem_clone_A" || target == "mem_clone_B" {
			t.Errorf("Master should not have intra-cluster related_to edge to %s", target)
		}
	}

	// 5. Reverse index: entity_Python's incoming mentions should include the master
	incomingMentions, found := eng.VGetIncoming(indexName, "entity_Python", "mentions")
	if !found {
		t.Fatal("entity_Python should have incoming mentions")
	}
	hasMaster := false
	for _, src := range incomingMentions {
		if src == masterID {
			hasMaster = true
			break
		}
	}
	if !hasMaster {
		t.Errorf("Master not found in entity_Python's incoming mentions. Got: %v", incomingMentions)
	}

	// 6. Master is not an island: should have at least 3 external edges (mentions, related_to, references)
	outRels := eng.VGetRelations(indexName, masterID)
	inRels := eng.VGetIncomingRelations(indexName, masterID)
	externalEdgeCount := 0
	for relType, targets := range outRels {
		if relType == "derived_from" || relType == "consolidated_into" {
			continue
		}
		externalEdgeCount += len(targets)
	}
	for _, sources := range inRels {
		externalEdgeCount += len(sources)
	}
	if externalEdgeCount < 3 {
		t.Errorf("Master should have at least 3 external edges, got %d", externalEdgeCount)
	}

	t.Log("✅ Consolidation Edge Transfer Test Passed Successfully!")
}

func TestConsolidationBasicMode(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "mcp_memory"
	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)

	// 5 clone memories with DIFFERENT content (same vector for clustering).
	// mem_central has graph edges — should be picked as master in basic mode.
	eng.VAdd(indexName, "mem_central", []float32{1.0, 0.0}, map[string]any{
		"content": "Python is a versatile language used in data science and web development",
		"type":    "working_memory",
	})
	eng.VAdd(indexName, "mem_b", []float32{1.0, 0.0}, map[string]any{
		"content": "Python is used a lot",
	})
	eng.VAdd(indexName, "mem_c", []float32{1.0, 0.0}, map[string]any{
		"content": "Python good language",
	})
	eng.VAdd(indexName, "mem_d", []float32{1.0, 0.0}, map[string]any{
		"content": "I like Python",
	})
	eng.VAdd(indexName, "mem_e", []float32{1.0, 0.0}, map[string]any{
		"content": "Python is popular",
	})

	// Give mem_central graph connections — it's the most central member.
	eng.VAdd(indexName, "entity_DS", []float32{0.3, 0.7}, map[string]any{"name": "DataScience", "type": "entity"})
	eng.VAdd(indexName, "entity_Web", []float32{0.4, 0.6}, map[string]any{"name": "WebDev", "type": "entity"})
	eng.VLink(indexName, "mem_central", "entity_DS", "mentions", "mentioned_in", 1.0, nil)
	eng.VLink(indexName, "mem_central", "entity_Web", "mentions", "mentioned_in", 1.0, nil)

	// Gardener with nil LLM (basic mode).
	gardener := NewGardener(eng, nil, Config{Enabled: true, Interval: 1 * time.Hour})

	clusters := gardener.findRedundantClusters(indexName, 0.90, 4)
	if len(clusters) != 1 {
		t.Fatalf("Expected 1 cluster, found %d", len(clusters))
	}

	// Consolidate WITHOUT LLM — should use deterministic fallback.
	gardener.consolidateCluster(indexName, clusters[0])

	// Find master via consolidated_into link.
	masterLinks, found := eng.VGetLinks(indexName, "mem_central", "consolidated_into")
	if !found || len(masterLinks) != 1 {
		t.Fatalf("Failed to find master after basic consolidation")
	}
	masterID := masterLinks[0]

	master, err := eng.VGet(indexName, masterID)
	if err != nil {
		t.Fatalf("Master node not found: %v", err)
	}

	// The master content should be mem_central's content (most graph-central member).
	masterContent, _ := master.Metadata["content"].(string)
	if masterContent != "Python is a versatile language used in data science and web development" {
		t.Errorf("Expected master content from mem_central (most central), got: %s", masterContent)
	}

	// Verify master is a consolidated_memory.
	if master.Metadata["type"] != "consolidated_memory" {
		t.Errorf("Expected type=consolidated_memory, got %v", master.Metadata["type"])
	}

	// Verify old memories are archived.
	oldMem, _ := eng.VGet(indexName, "mem_central")
	if archived, ok := oldMem.Metadata["_archived"].(bool); !ok || !archived {
		t.Errorf("mem_central should be archived")
	}

	// Verify edge transfer still works (mem_central had edges to entity_DS and entity_Web).
	mentionsLinks, _ := eng.VGetLinks(indexName, masterID, "mentions")
	if len(mentionsLinks) != 2 {
		t.Errorf("Expected 2 mentions links on master (basic mode edge transfer), got %d", len(mentionsLinks))
	}

	t.Log("✅ Consolidation Basic Mode Test Passed Successfully!")
}

func TestDetectUserPreferences(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "mcp_memory"
	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)

	// Memories tagged as user interactions.
	eng.VAdd(indexName, "mem_ui_1", []float32{0.5, 0.5}, map[string]any{
		"content":     "User prefers concise technical explanations",
		"tags":        []any{"user_interaction"},
		"_created_at": float64(time.Now().Add(-10 * 24 * time.Hour).Unix()),
	})
	eng.VAdd(indexName, "mem_ui_2", []float32{0.6, 0.4}, map[string]any{
		"content":     "User scheduled standup at 2pm instead of 9am",
		"tags":        []any{"user_interaction"},
		"_created_at": float64(time.Now().Add(-5 * 24 * time.Hour).Unix()),
	})
	eng.VAdd(indexName, "mem_obs_1", []float32{0.4, 0.6}, map[string]any{
		"content":     "User asked to avoid jargon in reports",
		"tags":        []any{"observation"},
		"_created_at": float64(time.Now().Add(-2 * 24 * time.Hour).Unix()),
	})

	// Memories WITHOUT tags (should be excluded).
	eng.VAdd(indexName, "mem_other_1", []float32{0.3, 0.7}, map[string]any{
		"content": "Some random memory",
	})
	eng.VAdd(indexName, "mem_other_2", []float32{0.7, 0.3}, map[string]any{
		"content": "Another memory without tags",
	})

	mockBrain := &MockLLM{
		PreferenceReply: `{
			"preferences_discovered": {
				"communication_style": "concise and technical",
				"preferred_meeting_time": "afternoon",
				"reporting_style": "no jargon"
			},
			"recommendation": "Always use concise language. Schedule meetings after 1pm. Use plain language in reports."
		}`,
	}
	gardener := NewGardener(eng, mockBrain, Config{Enabled: true, Interval: time.Hour})

	gardener.detectUserPreferences(indexName)

	if !mockBrain.Called {
		t.Errorf("LLM was never called during user preference detection")
	}

	// Find the user_profile_insight node.
	allIDs, _, _ := eng.VGetIDsByCursor(indexName, 0, 500)
	var insightIDs []string
	for _, id := range allIDs {
		if strings.HasPrefix(id, "user_profile_") {
			insightIDs = append(insightIDs, id)
		}
	}

	if len(insightIDs) != 1 {
		t.Fatalf("Expected 1 user_profile_insight, found %d", len(insightIDs))
	}

	insight, _ := eng.VGet(indexName, insightIDs[0])

	// Verify type.
	if insight.Metadata["type"] != "user_profile_insight" {
		t.Errorf("Expected type=user_profile_insight, got %v", insight.Metadata["type"])
	}

	// Verify status.
	if insight.Metadata["status"] != "active" {
		t.Errorf("Expected status=active, got %v", insight.Metadata["status"])
	}

	// Verify confidence (3 memories / 10 = 0.3).
	conf, _ := insight.Metadata["confidence"].(float64)
	if conf < 0.2 || conf > 0.4 {
		t.Errorf("Expected confidence ~0.3 (3 memories), got %v", conf)
	}

	// Verify preferences are stored.
	prefs, ok := insight.Metadata["preferences"].(map[string]string)
	if !ok {
		t.Fatalf("Expected preferences to be a map[string]string, got %T", insight.Metadata["preferences"])
	}
	if prefs["communication_style"] != "concise and technical" {
		t.Errorf("Expected communication_style=concise and technical, got %v", prefs["communication_style"])
	}

	// Verify recommendation.
	content, _ := insight.Metadata["content"].(string)
	if !strings.Contains(content, "concise language") {
		t.Errorf("Expected recommendation to mention concise language: %s", content)
	}

	// Verify graph links to source memories.
	links, found := eng.VGetLinks(indexName, insightIDs[0], "derived_from_interactions")
	if !found || len(links) != 3 {
		t.Errorf("Expected 3 derived_from_interactions links, got %d", len(links))
	}

	// Verify untagged memories are NOT linked.
	for _, link := range links {
		if link == "mem_other_1" || link == "mem_other_2" {
			t.Errorf("Untagged memory %s should not be linked to insight", link)
		}
	}

	t.Log("✅ User Preferences Detection Test Passed Successfully!")
}

func TestDetectUserPreferencesNoData(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "mcp_memory"
	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)

	// Memories with no user_interaction/observation tags.
	eng.VAdd(indexName, "mem_a", []float32{0.5, 0.5}, map[string]any{"content": "Some memory"})
	eng.VAdd(indexName, "mem_b", []float32{0.6, 0.4}, map[string]any{"content": "Another memory", "tags": []any{"random"}})

	gardener := NewGardener(eng, &MockLLM{}, Config{Enabled: true, Interval: time.Hour})
	gardener.detectUserPreferences(indexName)

	// Should not create any user_profile_insight nodes.
	allIDs, _, _ := eng.VGetIDsByCursor(indexName, 0, 500)
	for _, id := range allIDs {
		if strings.HasPrefix(id, "user_profile_") {
			t.Errorf("Should not create user_profile_insight when no tagged memories exist")
		}
	}

	t.Log("✅ User Preferences No Data Test Passed Successfully!")
}

func TestDetectRepeatedFailures(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "mcp_memory"
	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)

	now := float64(time.Now().Unix())

	// 5 failures for action "search_file" (should trigger pattern detection).
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("fail_search_%d", i)
		eng.VAdd(indexName, id, []float32{0.5, 0.5}, map[string]any{
			"content":     "File not found: /data/report.csv",
			"type":        "agent_action",
			"status":      "failed",
			"action":      "search_file",
			"_created_at": now - float64(i*3600),
		})
	}

	// 4 failures for action "query_db" (should trigger).
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("fail_db_%d", i)
		eng.VAdd(indexName, id, []float32{0.3, 0.7}, map[string]any{
			"content":     "Connection refused: PostgreSQL on localhost:5432",
			"type":        "agent_action",
			"status":      "failed",
			"action":      "query_db",
			"_created_at": now - float64(i*1800),
		})
	}

	// 2 failures for action "rare_error" (should NOT trigger — too few).
	for i := 0; i < 2; i++ {
		id := fmt.Sprintf("fail_rare_%d", i)
		eng.VAdd(indexName, id, []float32{0.1, 0.9}, map[string]any{
			"content":     "Random transient error",
			"type":        "agent_action",
			"status":      "failed",
			"action":      "rare_error",
			"_created_at": now,
		})
	}

	// Non-action memory (should be excluded).
	eng.VAdd(indexName, "mem_other", []float32{0.8, 0.2}, map[string]any{
		"content": "Some regular memory",
		"type":    "memory",
	})

	mockBrain := &MockLLM{
		FailureReply: `{"pattern": "Agent repeatedly tries to read a non-existent file", "root_cause": "File path is hardcoded but file does not exist in this environment", "recommendation": "Add file existence check (os.path.exists) before attempting to read.", "severity": "high"}`,
	}
	gardener := NewGardener(eng, mockBrain, Config{Enabled: true, Interval: time.Hour})

	gardener.detectRepeatedFailures(indexName)

	if !mockBrain.Called {
		t.Errorf("LLM was never called during failure detection")
	}

	// Find failure_pattern nodes.
	allIDs, _, _ := eng.VGetIDsByCursor(indexName, 0, 500)
	var patternIDs []string
	for _, id := range allIDs {
		if strings.HasPrefix(id, "failure_pattern_") {
			patternIDs = append(patternIDs, id)
		}
	}

	// Should create 2 patterns (search_file + query_db), NOT rare_error.
	if len(patternIDs) != 2 {
		t.Fatalf("Expected 2 failure_pattern nodes, found %d", len(patternIDs))
	}

	// Verify one of the patterns.
	found := false
	for _, pid := range patternIDs {
		pat, _ := eng.VGet(indexName, pid)
		if pat.Metadata["type"] != "failure_pattern" {
			t.Errorf("Expected type=failure_pattern, got %v", pat.Metadata["type"])
		}
		if pat.Metadata["severity"] == "high" {
			found = true
		}
		content, _ := pat.Metadata["content"].(string)
		if !strings.Contains(content, "existence check") {
			t.Errorf("Expected recommendation about file check: %s", content)
		}
	}
	if !found {
		t.Errorf("Expected at least one pattern with severity=high")
	}

	// Verify graph links from pattern to failures.
	for _, pid := range patternIDs {
		links, found := eng.VGetLinks(indexName, pid, "failure_pattern_of")
		if !found || len(links) < 3 {
			t.Errorf("Expected >=3 failure_pattern_of links from %s, got %d", pid, len(links))
		}
	}

	// Verify dedup: second call should not create new patterns.
	gardener.detectRepeatedFailures(indexName)
	allIDs2, _, _ := eng.VGetIDsByCursor(indexName, 0, 500)
	var patternIDs2 []string
	for _, id := range allIDs2 {
		if strings.HasPrefix(id, "failure_pattern_") {
			patternIDs2 = append(patternIDs2, id)
		}
	}
	if len(patternIDs2) != 2 {
		t.Errorf("Dedup failed: expected 2 patterns after second call, got %d", len(patternIDs2))
	}

	t.Log("✅ Repeated Failures Detection Test Passed Successfully!")
}

func TestDetectRepeatedFailuresNoData(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "mcp_memory"
	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)

	// Only regular memories, no agent_action nodes.
	eng.VAdd(indexName, "mem_a", []float32{0.5, 0.5}, map[string]any{"content": "Some memory", "type": "memory"})
	eng.VAdd(indexName, "mem_b", []float32{0.6, 0.4}, map[string]any{"content": "Another memory", "type": "memory"})

	gardener := NewGardener(eng, &MockLLM{}, Config{Enabled: true, Interval: time.Hour})
	gardener.detectRepeatedFailures(indexName)

	allIDs, _, _ := eng.VGetIDsByCursor(indexName, 0, 500)
	for _, id := range allIDs {
		if strings.HasPrefix(id, "failure_pattern_") {
			t.Errorf("Should not create failure_pattern when no agent_action failures exist")
		}
	}

	t.Log("✅ Repeated Failures No Data Test Passed Successfully!")
}

func TestDetectKnowledgeEvolution(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "mcp_memory"
	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)

	// High-centrality entity: 20 incoming mentions (in-degree >= 15 → triggers).
	eng.VAdd(indexName, "entity_QC", []float32{1.0, 0.0}, map[string]any{
		"name": "Quantum Computing", "type": "entity",
	})
	for i := 0; i < 20; i++ {
		memID := fmt.Sprintf("mem_qc_%d", i)
		eng.VAdd(indexName, memID, []float32{0.5, 0.5}, map[string]any{
			"content": fmt.Sprintf("Memory about quantum computing topic %d", i),
		})
		eng.VLink(indexName, memID, "entity_QC", "mentions", "mentioned_in", 1.0, nil)
	}

	// Low-centrality entity: only 3 incoming edges (should NOT trigger).
	eng.VAdd(indexName, "entity_low", []float32{0.0, 1.0}, map[string]any{
		"name": "Obscure Topic", "type": "entity",
	})
	for i := 0; i < 3; i++ {
		memID := fmt.Sprintf("mem_low_%d", i)
		eng.VAdd(indexName, memID, []float32{0.3, 0.7}, map[string]any{"content": "low topic memory"})
		eng.VLink(indexName, memID, "entity_low", "mentions", "mentioned_in", 1.0, nil)
	}

	// Non-entity with high in-degree (should NOT trigger).
	eng.VAdd(indexName, "mem_popular", []float32{0.7, 0.3}, map[string]any{
		"content": "Popular memory", "type": "memory",
	})
	for i := 0; i < 20; i++ {
		memID := fmt.Sprintf("mem_pop_%d", i)
		eng.VAdd(indexName, memID, []float32{0.5, 0.5}, map[string]any{"content": "link to popular"})
		eng.VLink(indexName, memID, "mem_popular", "mentions", "mentioned_in", 1.0, nil)
	}

	mockBrain := &MockLLM{
		EvolutionReply: `{
			"timeline": "30 days ago: initial exploration of quantum algorithms. 15 days ago: deep dive into quantum gates. Today: understanding of quantum error correction.",
			"competency_level": "intermediate",
			"knowledge_gaps": ["quantum entanglement protocols", "topological quantum computing"],
			"next_steps": "Study Shor's algorithm implementation details and explore quantum hardware constraints."
		}`,
	}
	gardener := NewGardener(eng, mockBrain, Config{Enabled: true, Interval: time.Hour})

	gardener.detectKnowledgeEvolution(indexName)

	if !mockBrain.Called {
		t.Errorf("LLM was never called during knowledge evolution detection")
	}

	// Find knowledge_evolution nodes.
	allIDs, _, _ := eng.VGetIDsByCursor(indexName, 0, 1000)
	var evoIDs []string
	for _, id := range allIDs {
		if strings.HasPrefix(id, "knowledge_evolution_") {
			evoIDs = append(evoIDs, id)
		}
	}

	if len(evoIDs) != 1 {
		t.Fatalf("Expected 1 knowledge_evolution node (only entity_QC), found %d", len(evoIDs))
	}

	evo, _ := eng.VGet(indexName, evoIDs[0])

	// Verify type.
	if evo.Metadata["type"] != "knowledge_evolution" {
		t.Errorf("Expected type=knowledge_evolution, got %v", evo.Metadata["type"])
	}

	// Verify competency level.
	if evo.Metadata["competency_level"] != "intermediate" {
		t.Errorf("Expected competency_level=intermediate, got %v", evo.Metadata["competency_level"])
	}

	// Verify topic.
	if evo.Metadata["topic"] != "Quantum Computing" {
		t.Errorf("Expected topic=Quantum Computing, got %v", evo.Metadata["topic"])
	}

	// Verify knowledge_gaps.
	gaps, ok := evo.Metadata["knowledge_gaps"].([]any)
	if !ok || len(gaps) != 2 {
		t.Errorf("Expected 2 knowledge_gaps, got %v", evo.Metadata["knowledge_gaps"])
	}

	// Verify timeline.
	timeline, _ := evo.Metadata["timeline"].(string)
	if !strings.Contains(timeline, "30 days ago") || !strings.Contains(timeline, "Today") {
		t.Errorf("Timeline should describe 3 phases: %s", timeline)
	}

	// Verify graph link.
	evoLinks, found := eng.VGetLinks(indexName, evoIDs[0], "evolution_of")
	if !found || len(evoLinks) != 1 || evoLinks[0] != "entity_QC" {
		t.Errorf("Expected evolution_of link to entity_QC, got %v", evoLinks)
	}

	// Verify dedup: second call should not create new nodes.
	gardener.detectKnowledgeEvolution(indexName)
	allIDs2, _, _ := eng.VGetIDsByCursor(indexName, 0, 1000)
	var evoIDs2 []string
	for _, id := range allIDs2 {
		if strings.HasPrefix(id, "knowledge_evolution_") {
			evoIDs2 = append(evoIDs2, id)
		}
	}
	if len(evoIDs2) != 1 {
		t.Errorf("Dedup failed: expected 1 evolution node after second call, got %d", len(evoIDs2))
	}

	t.Log("✅ Knowledge Evolution Detection Test Passed Successfully!")
}

func TestDetectKnowledgeEvolutionLowDegree(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	defer eng.Close()

	indexName := "mcp_memory"
	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)

	// Entity with only 5 incoming edges (below threshold of 15).
	eng.VAdd(indexName, "entity_small", []float32{1.0, 0.0}, map[string]any{
		"name": "Small Topic", "type": "entity",
	})
	for i := 0; i < 5; i++ {
		memID := fmt.Sprintf("mem_small_%d", i)
		eng.VAdd(indexName, memID, []float32{0.5, 0.5}, map[string]any{"content": "small memory"})
		eng.VLink(indexName, memID, "entity_small", "mentions", "mentioned_in", 1.0, nil)
	}

	gardener := NewGardener(eng, &MockLLM{}, Config{Enabled: true, Interval: time.Hour})
	gardener.detectKnowledgeEvolution(indexName)

	allIDs, _, _ := eng.VGetIDsByCursor(indexName, 0, 500)
	for _, id := range allIDs {
		if strings.HasPrefix(id, "knowledge_evolution_") {
			t.Errorf("Should not create knowledge_evolution for low-centrality entity")
		}
	}

	t.Log("✅ Knowledge Evolution Low Degree Test Passed Successfully!")
}
