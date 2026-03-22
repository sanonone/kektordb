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
}

func (m *MockLLM) Chat(systemPrompt, userQuery string) (string, error) {
	m.Called = true
	m.ReceivedPrompt = userQuery
	if strings.Contains(systemPrompt, "contradict") && m.ContradictionReply != "" {
		return m.ContradictionReply, nil
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
		ContradictionReply: `{"contradiction": true, "reason": "One states Python is fast, the other states it is slow"}`,
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
	gardener.detectSentimentShifts(indexName)

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
	gardener.detectSentimentShifts(indexName)
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
