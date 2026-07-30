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
