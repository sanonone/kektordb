package mcp

import "testing"

// Test di A3: assess_belief (MCP) e VBeliefState (engine, dietro
// POST /belief-assessment) devono restituire GLI STESSI numeri.
//
// Pre-fix esistevano due implementazioni con formule diverse: per dati
// identici l'MCP riportava 0.686 dove l'engine riportava 0.400 (credenza di un
// anno con contraddizioni esplicite). Il pilastro "stability" dell'MCP non era
// nemmeno una media: era (eta' del piu' vecchio)/N.
func TestAssessBeliefMatchesEngine(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	const idx = "mcp_memory"
	// NB: un vettore di soli zeri NON va bene: CosineDistance restituisce
	// distanza 1.0 per vettori nulli, e consensus risulterebbe 0 per artefatto.
	vec := make([]float32, 384)
	for i := range vec {
		vec[i] = 0.1
	}

	// Evidenze: 6 memorie coerenti, di cui una con contraddizioni nel grafo.
	for i := 0; i < 6; i++ {
		id := "belief_" + itoaMCP(i)
		eng.VAdd(idx, id, vec, map[string]any{
			"content":       "The server is in Europe",
			"type":          "memory",
			"_created_at":   float64(1760000000),
			"_access_count": 2,
		})
	}
	// Un nodo che contraddice il primo (friction esplicita nel grafo).
	eng.VLink(idx, "belief_1", "belief_0", "contradicts", "contradicted_by", 1.0, nil)

	_, res, err := svc.AssessBelief(nil, nil, AssessBeliefArgs{
		Query:     "where is the server",
		IndexName: idx,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("AssessBelief: %v", err)
	}

	// Lo stesso calcolo, direttamente sull'engine.
	vecQuery, err := svc.embedder.Embed("where is the server")
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	state, err := eng.VBeliefState(idx, vecQuery, 10, eng.GetEpistemicConfig(idx))
	if err != nil {
		t.Fatalf("VBeliefState: %v", err)
	}

	t.Logf("MCP    : conf=%.6f cons=%.4f stab=%.4f fric=%.4f state=%s verdict=%s",
		res.Confidence, res.Consensus, res.Stability, res.Friction, res.State, res.Verdict)
	t.Logf("ENGINE : conf=%.6f cons=%.4f stab=%.4f fric=%.4f state=%s",
		state.Confidence, state.Evidence.Consensus.Score,
		state.Evidence.Stability.Score, state.Evidence.Friction.Score, state.State)

	if res.Confidence != state.Confidence {
		t.Errorf("confidence diverge: MCP %.6f != engine %.6f", res.Confidence, state.Confidence)
	}
	if res.Consensus != state.Evidence.Consensus.Score {
		t.Errorf("consensus diverge: MCP %.6f != engine %.6f", res.Consensus, state.Evidence.Consensus.Score)
	}
	if res.Stability != state.Evidence.Stability.Score {
		t.Errorf("stability diverge: MCP %.6f != engine %.6f", res.Stability, state.Evidence.Stability.Score)
	}
	if res.Friction != state.Evidence.Friction.Score {
		t.Errorf("friction diverge: MCP %.6f != engine %.6f", res.Friction, state.Evidence.Friction.Score)
	}
	if res.State != state.State {
		t.Errorf("state diverge: MCP %q != engine %q", res.State, state.State)
	}
}

// TestAssessBeliefNoEvidence: senza candidati il risultato è un esito normale,
// non un errore, e non inventa una confidenza.
func TestAssessBeliefNoEvidence(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)

	_, res, err := svc.AssessBelief(nil, nil, AssessBeliefArgs{
		Query:     "something never stored",
		IndexName: "mcp_memory",
		Limit:     5,
	})
	if err != nil {
		t.Fatalf("AssessBelief: %v", err)
	}
	t.Logf("senza evidenze: conf=%.2f verdict=%s msg=%q", res.Confidence, res.Verdict, res.Message)
	if res.Confidence != 0 {
		t.Errorf("confidence = %v, want 0 (nessuna evidenza)", res.Confidence)
	}
	if res.Verdict != "no_evidence" {
		t.Errorf("verdict = %q, want no_evidence", res.Verdict)
	}
}

// TestAssessBeliefEvidenceCarriesPerNodeData: l'evidenza deve riportare i dati
// per nodo del motore (score, eta', contraddizioni), non l'euristica "stance"
// che richiedeva metadata inesistenti.
func TestAssessBeliefEvidenceCarriesPerNodeData(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	const idx = "mcp_memory"
	vec := make([]float32, 384)
	for i := range vec {
		vec[i] = 0.1
	}

	eng.VAdd(idx, "ev1", vec, map[string]any{
		"content": "fact one", "type": "memory", "_created_at": float64(1760000000),
	})
	eng.VAdd(idx, "ev2", vec, map[string]any{
		"content": "fact two", "type": "memory", "_created_at": float64(1760000000),
	})

	_, res, err := svc.AssessBelief(nil, nil, AssessBeliefArgs{Query: "fact", IndexName: idx, Limit: 10})
	if err != nil {
		t.Fatalf("AssessBelief: %v", err)
	}
	if len(res.Evidence) == 0 {
		t.Fatal("nessuna evidenza restituita")
	}
	for _, e := range res.Evidence {
		if e.MemoryID == "" {
			t.Error("evidence senza memory_id")
		}
		if e.CreatedAt == 0 {
			t.Errorf("evidence %s senza created_at (il campo deve essere popolato)", e.MemoryID)
		}
	}
	t.Logf("evidenze: %d, primo: id=%s created=%d score=%.4f",
		len(res.Evidence), res.Evidence[0].MemoryID, res.Evidence[0].CreatedAt, res.Evidence[0].Score)
}

func itoaMCP(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
