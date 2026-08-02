package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestInstallHermes(t *testing.T) {
	origHome := userHomeDir
	origExec := osExecutable
	origWriteFile := writeFileFn
	origReadFile := readFileFn
	origStat := statFn
	origMkdirAll := mkdirAllFn
	defer func() {
		userHomeDir = origHome
		osExecutable = origExec
		writeFileFn = origWriteFile
		readFileFn = origReadFile
		statFn = origStat
		mkdirAllFn = origMkdirAll
	}()

	tmpDir := t.TempDir()
	userHomeDir = func() (string, error) { return tmpDir, nil }
	osExecutable = func() (string, error) { return "/usr/local/bin/kektordb", nil }

	// Use real file operations for the isolated temp dir.
	writeFileFn = os.WriteFile
	readFileFn = os.ReadFile
	statFn = os.Stat
	mkdirAllFn = os.MkdirAll

	// Create a Hermes config with one provider.
	configDir := filepath.Join(tmpDir, ".hermes")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	initialConfig := `providers:
  opencode-go:
    base_url: "https://opencode.ai/zen/go/v1"
    model: "deepseek-v4-flash"
    api_key_env: "OPENCODE_GO_API_KEY"
`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := Install("hermes", "auto")
	if err != nil {
		t.Fatalf("install hermes: %v", err)
	}
	if result.Agent != "hermes" {
		t.Errorf("agent = %q, want hermes", result.Agent)
	}
	if result.Files != 3 {
		t.Errorf("files = %d, want 3", result.Files)
	}

	// Verify config.yaml contains the MCP entry.
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "mcp_servers:") {
		t.Error("missing mcp_servers section")
	}
	if !strings.Contains(content, "kektordb:") {
		t.Error("missing kektordb entry")
	}
	if !strings.Contains(content, "/usr/local/bin/kektordb") {
		t.Error("missing kektordb command")
	}
	if !strings.Contains(content, "connect_timeout: 60") {
		t.Error("missing connect_timeout")
	}
	if !strings.Contains(content, "--cognitive-config") {
		t.Error("missing --cognitive-config arg")
	}

	// Verify the YAML structure round-trips.
	var config map[string]any
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	servers, ok := config["mcp_servers"].(map[string]any)
	if !ok {
		t.Fatal("mcp_servers is not a map")
	}
	kektordb, ok := servers["kektordb"].(map[string]any)
	if !ok {
		t.Fatal("kektordb entry is not a map")
	}
	if kektordb["command"] != "/usr/local/bin/kektordb" {
		t.Errorf("command = %v, want /usr/local/bin/kektordb", kektordb["command"])
	}
	if kektordb["enabled"] != true {
		t.Errorf("enabled = %v, want true", kektordb["enabled"])
	}
	args, ok := kektordb["args"].([]any)
	if !ok {
		t.Fatal("args is not a slice")
	}
	if len(args) == 0 || args[0] != "--mcp" {
		t.Errorf("args[0] = %v, want --mcp", args[0])
	}

	// Verify cognitive.yaml.
	cognitivePath := filepath.Join(tmpDir, ".hermes", "kektordb", "cognitive.yaml")
	cognitiveData, err := os.ReadFile(cognitivePath)
	if err != nil {
		t.Fatalf("read cognitive config: %v", err)
	}
	cognitiveContent := string(cognitiveData)
	if !strings.Contains(cognitiveContent, `base_url: "https://opencode.ai/zen/go/v1"`) {
		t.Error("cognitive config missing base_url")
	}
	if !strings.Contains(cognitiveContent, `model: "deepseek-v4-flash"`) {
		t.Error("cognitive config missing model")
	}
	if !strings.Contains(cognitiveContent, "${OPENCODE_GO_API_KEY}") {
		t.Error("cognitive config missing api_key env expansion")
	}

	// Verify skill file.
	skillPath := filepath.Join(tmpDir, ".hermes", "skills", "memory", "kektordb-mcp", "SKILL.md")
	skillData, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read skill: %v", err)
	}
	if !strings.Contains(string(skillData), "KektorDB") {
		t.Error("skill file missing KektorDB")
	}
}

func TestInstallHermes_Idempotent(t *testing.T) {
	origHome := userHomeDir
	origExec := osExecutable
	origWriteFile := writeFileFn
	origReadFile := readFileFn
	origStat := statFn
	origMkdirAll := mkdirAllFn
	defer func() {
		userHomeDir = origHome
		osExecutable = origExec
		writeFileFn = origWriteFile
		readFileFn = origReadFile
		statFn = origStat
		mkdirAllFn = origMkdirAll
	}()

	tmpDir := t.TempDir()
	userHomeDir = func() (string, error) { return tmpDir, nil }
	osExecutable = func() (string, error) { return "/usr/local/bin/kektordb", nil }

	writeFileFn = os.WriteFile
	readFileFn = os.ReadFile
	statFn = os.Stat
	mkdirAllFn = os.MkdirAll

	configDir := filepath.Join(tmpDir, ".hermes")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	initialConfig := `providers:
  opencode-go:
    base_url: "https://opencode.ai/zen/go/v1"
    model: "deepseek-v4-flash"
`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatal(err)
	}

	first, err := Install("hermes", "")
	if err != nil {
		t.Fatalf("first install: %v", err)
	}
	if first.Files == 0 {
		t.Fatal("first install should modify files")
	}

	second, err := Install("hermes", "")
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if second.Files != first.Files-1 {
		t.Errorf("second install files = %d, want %d (only MCP config should be skipped)", second.Files, first.Files-1)
	}

	// Verify the MCP config was not overwritten with a different command.
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	count := strings.Count(content, "kektordb:")
	if count != 1 {
		t.Errorf("kektordb entry appears %d times, want 1", count)
	}
}

func TestInstallHermes_NoConfig(t *testing.T) {
	origHome := userHomeDir
	origStat := statFn
	defer func() {
		userHomeDir = origHome
		statFn = origStat
	}()

	tmpDir := t.TempDir()
	userHomeDir = func() (string, error) { return tmpDir, nil }
	statFn = os.Stat

	_, err := Install("hermes", "")
	if err == nil {
		t.Fatal("expected error when hermes config is missing")
	}
	if !strings.Contains(err.Error(), "hermes not found") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestDetectHermesProvider(t *testing.T) {
	origHome := userHomeDir
	origReadFile := readFileFn
	defer func() {
		userHomeDir = origHome
		readFileFn = origReadFile
	}()

	tmpDir := t.TempDir()
	userHomeDir = func() (string, error) { return tmpDir, nil }

	configPath := hermesConfigPath()
	configDir := filepath.Dir(configPath)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	readFileFn = os.ReadFile

	cfg := `providers:
  opencode-go:
    base_url: "https://opencode.ai/zen/go/v1"
    model: "deepseek-v4-flash"
    api_key_env: "OPENCODE_GO_API_KEY"
`
	if err := os.WriteFile(configPath, []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}

	provider, err := detectHermesProvider()
	if err != nil {
		t.Fatalf("detect provider: %v", err)
	}
	if provider.Name != "opencode-go" {
		t.Errorf("name = %q, want opencode-go", provider.Name)
	}
	if provider.BaseURL != "https://opencode.ai/zen/go/v1" {
		t.Errorf("base_url = %q", provider.BaseURL)
	}
	if provider.Model != "deepseek-v4-flash" {
		t.Errorf("model = %q", provider.Model)
	}
	if provider.APIKeyEnv != "OPENCODE_GO_API_KEY" {
		t.Errorf("api_key_env = %q", provider.APIKeyEnv)
	}
}

func TestDetectHermesProvider_FallbackEnv(t *testing.T) {
	origHome := userHomeDir
	origReadFile := readFileFn
	defer func() {
		userHomeDir = origHome
		readFileFn = origReadFile
	}()

	tmpDir := t.TempDir()
	userHomeDir = func() (string, error) { return tmpDir, nil }
	readFileFn = os.ReadFile

	configPath := hermesConfigPath()
	configDir := filepath.Dir(configPath)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := `providers:
  my-provider-2:
    base_url: "https://example.com/v1"
    model: "gpt-4"
`
	if err := os.WriteFile(configPath, []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}

	provider, err := detectHermesProvider()
	if err != nil {
		t.Fatalf("detect provider: %v", err)
	}
	if provider.APIKeyEnv != "MY_PROVIDER_2_API_KEY" {
		t.Errorf("api_key_env = %q, want MY_PROVIDER_2_API_KEY", provider.APIKeyEnv)
	}
}

func TestDetectHermesProvider_NoProviders(t *testing.T) {
	origHome := userHomeDir
	origReadFile := readFileFn
	defer func() {
		userHomeDir = origHome
		readFileFn = origReadFile
	}()

	tmpDir := t.TempDir()
	userHomeDir = func() (string, error) { return tmpDir, nil }
	readFileFn = os.ReadFile

	configPath := hermesConfigPath()
	configDir := filepath.Dir(configPath)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := `gateway:
  port: 8080
`
	if err := os.WriteFile(configPath, []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := detectHermesProvider()
	if err == nil {
		t.Fatal("expected error when no providers configured")
	}
}

func TestEnvNameFromProvider(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"opencode-go", "OPENCODE_GO"},
		{"openai", "OPENAI"},
		{"my.provider", "MY_PROVIDER"},
		{"anthropic:claude", "ANTHROPIC_CLAUDE"},
	}
	for _, tc := range cases {
		got := envNameFromProvider(tc.in)
		if got != tc.want {
			t.Errorf("envNameFromProvider(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
