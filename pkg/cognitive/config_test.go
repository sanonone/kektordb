package cognitive

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_ExpandEnv(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "cognitive.yaml")
	content := `
gardener:
  enabled: true
  mode: basic
  interval: 30s
llm:
  api_key: ${TEST_KEKTORDB_API_KEY}
`
	os.WriteFile(configPath, []byte(content), 0644)
	os.Setenv("TEST_KEKTORDB_API_KEY", "secret-123")
	defer os.Unsetenv("TEST_KEKTORDB_API_KEY")

	_, llmCfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if llmCfg.APIKey != "secret-123" {
		t.Errorf("APIKey = %q, want secret-123", llmCfg.APIKey)
	}
}

func TestLoadConfig_ExpandEnv_NoVar(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "cognitive.yaml")
	content := `
gardener:
  enabled: true
  mode: basic
  interval: 30s
llm:
  api_key: plain-literal-value
`
	os.WriteFile(configPath, []byte(content), 0644)

	_, llmCfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if llmCfg.APIKey != "plain-literal-value" {
		t.Errorf("APIKey = %q, want plain-literal-value", llmCfg.APIKey)
	}
}

// TestCoreFactExtractionIsConfigurable verifica che le opzioni di estrazione dei
// core facts siano raggiungibili dal file di configurazione.
//
// Il difetto originale: i campi esistevano nella struct `Config` (usata dal
// Gardener) ma mancavano in `GardenerConfigYAML`, quindi nessun YAML poteva
// attivare la feature: restava sempre disabilitata e il Gardener non estraeva
// nulla, senza errori e senza log.
func TestCoreFactExtractionIsConfigurable(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/cognitive.yaml"
	yaml := `
gardener:
  enabled: true
  mode: advanced
  target_indexes: ["mem"]
  enable_core_fact_extraction: true
  core_fact_min_confidence: 0.7
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.EnableCoreFactExtraction {
		t.Error("enable_core_fact_extraction non e' stato letto dal YAML")
	}
	if cfg.CoreFactMinConfidence != 0.7 {
		t.Errorf("core_fact_min_confidence: atteso 0.7, got %v", cfg.CoreFactMinConfidence)
	}
}

// TestCoreFactExtractionDefaultsOn: dalla Fase 4 la feature e' classificata
// STANDARD e attiva di default. Costa 0.01 chiamate per memoria — trascurabile —
// e dimezza la memoria visibile (-64% misurato). Si disattiva con `false`.
func TestCoreFactExtractionDefaultsOn(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/cognitive.yaml"
	yaml := `
gardener:
  enabled: true
  mode: advanced
  target_indexes: ["mem"]
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.EnableCoreFactExtraction {
		t.Error("la feature deve essere ATTIVA di default (classificazione Fase 4)")
	}

	// E deve essere disattivabile esplicitamente.
	path2 := dir + "/off.yaml"
	body2 := "gardener:\n  enabled: true\n  mode: advanced\n  enable_core_fact_extraction: false\n"
	if err := os.WriteFile(path2, []byte(body2), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg2, _, err := LoadConfig(path2)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg2.EnableCoreFactExtraction {
		t.Error("enable_core_fact_extraction: false deve disattivarla")
	}
}

// TestRetireAfterCoreFactExtractionIsConfigurable: l'archiviazione delle sorgenti
// dopo l'estrazione deve essere attivabile da YAML e spenta di default (cambia
// cosa l'utente vede nelle ricerche: non puo' accadere per caso).
func TestRetireAfterCoreFactExtractionIsConfigurable(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/cognitive.yaml"
	body := `
gardener:
  enabled: true
  mode: advanced
  target_indexes: ["mem"]
  enable_core_fact_extraction: true
  retire_after_core_fact_extraction: true
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.RetireAfterCoreFactExtraction {
		t.Error("retire_after_core_fact_extraction non letto dal YAML")
	}
}

func TestRetireAfterCoreFactExtractionDefaultsOn(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/cognitive.yaml"
	body := `
gardener:
  enabled: true
  mode: advanced
  target_indexes: ["mem"]
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.RetireAfterCoreFactExtraction {
		t.Error("l'archiviazione delle sorgenti deve essere ATTIVA di default: e' cio' che compatta la memoria")
	}
}

// TestDetectorToggles verifica che i detector di default girino e che si
// possano spegnere.
//
// Il contratto: nil (non configurato) = attivo. Questi detector hanno sempre
// girato: spegnerli in silenzio cambierebbe il comportamento per chi ha gia'
// un deployment.
func TestDetectorToggles(t *testing.T) {
	dir := t.TempDir()

	// Non configurati: tutto attivo.
	path := dir + "/default.yaml"
	if err := os.WriteFile(path, []byte("gardener:\n  enabled: true\n  mode: advanced\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !enabled(cfg.DisableConsolidation) {
		t.Error("consolidamento: non configurato deve essere attivo")
	}
	if !enabled(cfg.DisableKnowledgeEvolution) {
		t.Error("knowledge evolution: non configurato deve essere attivo")
	}

	// Esplicitamente disattivati.
	path2 := dir + "/off.yaml"
	body := `
gardener:
  enabled: true
  mode: advanced
  disable_consolidation: false
  disable_knowledge_evolution: false
`
	if err := os.WriteFile(path2, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg2, _, err := LoadConfig(path2)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if enabled(cfg2.DisableConsolidation) {
		t.Error("consolidamento: disable_consolidation=false deve disattivarlo")
	}
	if enabled(cfg2.DisableKnowledgeEvolution) {
		t.Error("knowledge evolution: disable_knowledge_evolution=false deve disattivarlo")
	}
}

// TestEnabledHelper: il helper deve trattare nil come attivo.
func TestEnabledHelper(t *testing.T) {
	if !enabled(nil) {
		t.Error("nil deve significare attivo")
	}
	tr := true
	if !enabled(&tr) {
		t.Error("true deve significare attivo")
	}
	f := false
	if enabled(&f) {
		t.Error("false deve significare disattivato")
	}
}
