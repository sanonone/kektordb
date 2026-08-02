package setup

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed plugins/hermes/*
var hermesFS embed.FS

const hermesSkillFile = "plugins/hermes/SKILL.md"

// hermesConfigPath returns the path to Hermes's config.yaml.
func hermesConfigPath() string {
	home, _ := userHomeDir()
	return filepath.Join(home, ".hermes", "config.yaml")
}

// hermesDataDir returns the Hermes data directory for KektorDB.
func hermesDataDir() string {
	home, _ := userHomeDir()
	return filepath.Join(home, ".hermes", "kektordb")
}

// hermesSkillDir returns the directory where Hermes skill files are stored.
func hermesSkillDir() string {
	home, _ := userHomeDir()
	return filepath.Join(home, ".hermes", "skills", "memory", "kektordb-mcp")
}

// hermesProvider holds the first LLM provider detected from Hermes config.
type hermesProvider struct {
	Name      string
	BaseURL   string
	Model     string
	APIKeyEnv string
}

// installHermes sets up KektorDB for the Hermes gateway.
func installHermes(embedderMode string) (*Result, error) {
	configPath := hermesConfigPath()

	// Hermes config must exist.
	if _, err := statFn(configPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("hermes not found: %s does not exist (install Hermes first)", configPath)
		}
		return nil, fmt.Errorf("stat hermes config: %w", err)
	}

	files := 0

	// Detect LLM provider to auto-generate cognitive config.
	provider, detectErr := detectHermesProvider()
	cognitivePath := filepath.Join(hermesDataDir(), "cognitive.yaml")

	// Build MCP entry. The cognitive config path is always included so the user
	// can fill it manually if provider detection fails.
	args := []string{"--mcp", "--tools", "agent"}
	if embedderMode != "" && embedderMode != "auto" {
		args = append(args, "--embedder", embedderMode)
	}
	args = append(args, "--cognitive-config", cognitivePath)

	entry := map[string]any{
		"command":         resolveKektordbCommand(),
		"args":            args,
		"connect_timeout": 60.0,
		"enabled":         true,
	}

	written, err := injectYAMLMCPEntry(configPath, "mcp_servers", "kektordb", entry)
	if err != nil {
		return nil, fmt.Errorf("inject MCP entry: %w", err)
	}
	if written {
		files++
	}

	// Write cognitive config.
	if detectErr == nil && provider.BaseURL != "" && provider.Model != "" {
		if err := writeHermesCognitiveConfig(cognitivePath, provider); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not write cognitive config: %v\n", err)
		} else {
			files++
		}
	} else if detectErr != nil {
		fmt.Fprintf(os.Stderr, "warning: could not detect Hermes LLM provider: %v\n", detectErr)
	}

	// Write skill file.
	skillPath, err := writeHermesSkill()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write Hermes skill: %v\n", err)
	} else if skillPath != "" {
		files++
	}

	return &Result{
		Agent:       "hermes",
		Destination: configPath,
		Files:       files,
	}, nil
}

// detectHermesProvider reads ~/.hermes/config.yaml and extracts the first
// configured provider.
func detectHermesProvider() (*hermesProvider, error) {
	data, err := readFileFn(hermesConfigPath())
	if err != nil {
		return nil, err
	}

	var config map[string]any
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse hermes config: %w", err)
	}

	providersRaw, ok := config["providers"]
	if !ok {
		return nil, fmt.Errorf("no providers section in hermes config")
	}

	providers, ok := providersRaw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("providers section is not a map")
	}

	if len(providers) == 0 {
		return nil, fmt.Errorf("no providers configured")
	}

	// Pick the first provider.
	for name, raw := range providers {
		provider := &hermesProvider{Name: name}
		vals, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if v, ok := vals["base_url"].(string); ok {
			provider.BaseURL = v
		}
		if v, ok := vals["model"].(string); ok {
			provider.Model = v
		}
		if v, ok := vals["api_key_env"].(string); ok {
			provider.APIKeyEnv = v
		}
		if provider.APIKeyEnv == "" {
			provider.APIKeyEnv = fmt.Sprintf("%s_API_KEY", envNameFromProvider(name))
		}
		return provider, nil
	}

	return nil, fmt.Errorf("could not parse any provider")
}

// envNameFromProvider converts a provider name like "opencode-go" to
// "OPENCODE_GO".
func envNameFromProvider(name string) string {
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.ToUpper(b.String())
}

// writeHermesCognitiveConfig writes the cognitive.yaml file for Hermes.
func writeHermesCognitiveConfig(path string, provider *hermesProvider) error {
	content := fmt.Sprintf(`gardener:
  enabled: true
  mode: "meta"
  interval: "30s"
  target_indexes: ["*"]
  adaptive_threshold: 200
  adaptive_min_interval: "90s"

auto_resolve:
  enabled: true

llm:
  base_url: %q
  model: %q
  api_key: "${%s}"
  temperature: 0.5
  max_tokens: 500
`, provider.BaseURL, provider.Model, provider.APIKeyEnv)

	if err := ensureConfigDir(path); err != nil {
		return err
	}
	return writeFileFn(path, []byte(content), 0644)
}

// writeHermesSkill writes the KektorDB skill file for Hermes.
func writeHermesSkill() (string, error) {
	data, err := hermesFS.ReadFile(hermesSkillFile)
	if err != nil {
		return "", err
	}

	skillDir := hermesSkillDir()
	if err := mkdirAllFn(skillDir, 0755); err != nil {
		return "", err
	}

	dest := filepath.Join(skillDir, "SKILL.md")
	if err := writeFileFn(dest, data, 0644); err != nil {
		return "", err
	}
	return dest, nil
}

// injectYAMLMCPEntry reads a YAML config file, ensures parentKey contains an
// entry for serverName, and writes it back. The returned bool is true if the
// file was modified (entry did not exist before).
func injectYAMLMCPEntry(configPath, parentKey, serverName string, entry map[string]any) (bool, error) {
	var config map[string]any
	data, err := readFileFn(configPath)
	if err != nil {
		return false, fmt.Errorf("read config: %w", err)
	}
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &config); err != nil {
			return false, fmt.Errorf("parse config: %w", err)
		}
	} else {
		config = make(map[string]any)
	}

	// Ensure parent key exists.
	parentRaw, ok := config[parentKey]
	if !ok {
		parentRaw = make(map[string]any)
		config[parentKey] = parentRaw
	}
	parent, ok := parentRaw.(map[string]any)
	if !ok {
		return false, fmt.Errorf("%s section is not a map", parentKey)
	}

	// Idempotent: skip if already present.
	if _, exists := parent[serverName]; exists {
		return false, nil
	}

	parent[serverName] = entry

	output, err := yaml.Marshal(config)
	if err != nil {
		return false, fmt.Errorf("marshal config: %w", err)
	}

	if err := ensureConfigDir(configPath); err != nil {
		return false, fmt.Errorf("create config dir: %w", err)
	}

	if err := writeFileFn(configPath, output, 0644); err != nil {
		return false, fmt.Errorf("write config: %w", err)
	}
	return true, nil
}
