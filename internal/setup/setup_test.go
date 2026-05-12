package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpi "github.com/sanonone/kektordb/internal/mcp"
)

// ─── resolveKektordbCommand ──────────────────────────────────────────────────

func TestResolveKektordbCommandNonEmpty(t *testing.T) {
	cmd := resolveKektordbCommand()
	if cmd == "" {
		t.Fatal("resolveKektordbCommand returned empty string")
	}
	// During tests, os.Executable returns the test binary path — should be absolute.
	if !filepath.IsAbs(cmd) {
		t.Logf("warning: path is not absolute: %s (may be expected in some test envs)", cmd)
	}
}

// ─── SupportedAgents ─────────────────────────────────────────────────────────

func TestSupportedAgentsNonEmpty(t *testing.T) {
	agents := SupportedAgents()
	if len(agents) == 0 {
		t.Fatal("SupportedAgents returned empty list")
	}
}

func TestSupportedAgentsUniqueNames(t *testing.T) {
	agents := SupportedAgents()
	seen := make(map[string]bool)
	for _, a := range agents {
		if seen[a.Name] {
			t.Errorf("duplicate agent name: %s", a.Name)
		}
		seen[a.Name] = true
	}
	if len(seen) < 5 {
		t.Errorf("expected at least 5 agents, got %d", len(seen))
	}
}

func TestSupportedAgentsHaveDescriptions(t *testing.T) {
	for _, a := range SupportedAgents() {
		if a.Name == "" {
			t.Error("agent has empty name")
		}
		if a.Description == "" {
			t.Errorf("agent %s has empty description", a.Name)
		}
	}
}

// ─── Install ─────────────────────────────────────────────────────────────────

func TestInstallUnknownAgent(t *testing.T) {
	_, err := Install("nonexistent-agent-xyz")
	if err == nil {
		t.Fatal("expected error for unknown agent")
	}
	if !strings.Contains(err.Error(), "unknown agent") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestInstallAllKnownAgents(t *testing.T) {
	// Each install should succeed (write config files to temp locations).
	// We override the home directory and other globals for isolation.
	origHome := userHomeDir
	origWriteFile := writeFileFn
	origReadFile := readFileFn
	origMkdirAll := mkdirAllFn
	origStat := statFn
	defer func() {
		userHomeDir = origHome
		writeFileFn = origWriteFile
		readFileFn = origReadFile
		mkdirAllFn = origMkdirAll
		statFn = origStat
	}()

	tmpDir := t.TempDir()
	userHomeDir = func() (string, error) { return tmpDir, nil }

	// Track written files for inspection.
	written := make(map[string][]byte)
	writeFileFn = func(name string, data []byte, perm os.FileMode) error {
		written[name] = append([]byte{}, data...)
		return os.WriteFile(name, data, perm)
	}
	readFileFn = func(name string) ([]byte, error) {
		if data, ok := written[name]; ok {
			return data, nil
		}
		return nil, os.ErrNotExist
	}
	mkdirAllFn = func(path string, perm os.FileMode) error {
		return os.MkdirAll(path, perm)
	}
	statFn = func(name string) (os.FileInfo, error) {
		if _, ok := written[name]; ok {
			return nil, nil // exists
		}
		return nil, os.ErrNotExist
	}

	// Also mock osExecutable to return a predictable path.
	origExec := osExecutable
	osExecutable = func() (string, error) { return "/usr/local/bin/kektordb", nil }
	defer func() { osExecutable = origExec }()

	for _, agent := range SupportedAgents() {
		t.Run(agent.Name, func(t *testing.T) {
			result, err := Install(agent.Name)
			if err != nil {
				t.Fatalf("Install(%q): %v", agent.Name, err)
			}
			if result == nil {
				t.Fatal("Install returned nil result")
			}
			if result.Agent != agent.Name {
				t.Errorf("result.Agent = %q, want %q", result.Agent, agent.Name)
			}
			if result.Files < 1 {
				t.Errorf("result.Files = %d, want >= 1", result.Files)
			}
			t.Logf("  → %s (%d files)", result.Destination, result.Files)
		})
	}

	// Idempotency: run each install again — should not fail.
	for _, agent := range SupportedAgents() {
		t.Run(agent.Name+"-idempotent", func(t *testing.T) {
			_, err := Install(agent.Name)
			if err != nil {
				t.Fatalf("Install(%q) second time: %v", agent.Name, err)
			}
		})
	}
}

// ─── injectMCPEntry ──────────────────────────────────────────────────────────

func TestInjectMCPEntryNewFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test_config.json")

	entry := map[string]any{
		"command": "/usr/local/bin/kektordb",
		"args":    []string{"--mcp", "--tools=agent"},
	}

	if err := injectMCPEntry(configPath, "mcpServers", "kektordb", entry); err != nil {
		t.Fatalf("injectMCPEntry: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var config map[string]json.RawMessage
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse config: %v", err)
	}

	var servers map[string]json.RawMessage
	if err := json.Unmarshal(config["mcpServers"], &servers); err != nil {
		t.Fatalf("parse mcpServers: %v", err)
	}

	if _, ok := servers["kektordb"]; !ok {
		t.Fatal("kektordb entry not found in mcpServers")
	}
}

func TestInjectMCPEntryIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test_config.json")

	entry := map[string]any{
		"command": "/usr/local/bin/kektordb",
		"args":    []string{"--mcp"},
	}

	if err := injectMCPEntry(configPath, "mcp_servers", "kektordb", entry); err != nil {
		t.Fatalf("first inject: %v", err)
	}

	// Second inject with the same key should be a no-op.
	if err := injectMCPEntry(configPath, "mcp_servers", "kektordb", map[string]any{
		"command": "/different/path", "args": []string{"--different"},
	}); err != nil {
		t.Fatalf("second inject: %v", err)
	}

	// Verify the first entry is preserved (not overwritten).
	data, _ := os.ReadFile(configPath)
	var config map[string]json.RawMessage
	json.Unmarshal(data, &config)
	var servers map[string]json.RawMessage
	json.Unmarshal(config["mcp_servers"], &servers)

	var got map[string]any
	json.Unmarshal(servers["kektordb"], &got)
	if got["command"] != "/usr/local/bin/kektordb" {
		t.Errorf("entry was overwritten: command = %v", got["command"])
	}
}

func TestInjectMCPEntryPreservesOtherKeys(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test_config.json")

	// Write initial config with another server.
	initial := map[string]any{
		"mcpServers": map[string]any{
			"other-server": map[string]any{
				"command": "/usr/bin/other",
			},
		},
	}
	initialJSON, _ := json.MarshalIndent(initial, "", "  ")
	os.WriteFile(configPath, initialJSON, 0644)

	// Inject kektordb.
	entry := map[string]any{
		"command": "/usr/local/bin/kektordb",
		"args":    []string{"--mcp"},
	}
	if err := injectMCPEntry(configPath, "mcpServers", "kektordb", entry); err != nil {
		t.Fatalf("injectMCPEntry: %v", err)
	}

	data, _ := os.ReadFile(configPath)
	var config map[string]json.RawMessage
	json.Unmarshal(data, &config)
	var servers map[string]json.RawMessage
	json.Unmarshal(config["mcpServers"], &servers)

	if _, ok := servers["other-server"]; !ok {
		t.Error("other-server entry was removed")
	}
	if _, ok := servers["kektordb"]; !ok {
		t.Error("kektordb entry was not added")
	}
}

// ─── standardMCPEntry ────────────────────────────────────────────────────────

func TestStandardMCPEntryFormat(t *testing.T) {
	entry := standardMCPEntry()
	if _, ok := entry["command"]; !ok {
		t.Error("missing 'command' key")
	}
	if args, ok := entry["args"].([]string); !ok {
		t.Error("missing or invalid 'args' key")
	} else {
		if len(args) < 2 {
			t.Error("args should have at least 2 elements")
		}
		foundTools := false
		for _, a := range args {
			if strings.HasPrefix(a, "--tools=") {
				foundTools = true
			}
		}
		if !foundTools {
			t.Error("args should contain --tools= flag")
		}
	}
}

// ─── Codex TOML ──────────────────────────────────────────────────────────────

func TestCodexTOMLFormat(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, ".codex", "config.toml")

	origHome := userHomeDir
	origExec := osExecutable
	userHomeDir = func() (string, error) { return tmpDir, nil }
	osExecutable = func() (string, error) { return "/usr/local/bin/kektordb", nil }
	defer func() {
		userHomeDir = origHome
		osExecutable = origExec
	}()

	result, err := installCodex()
	if err != nil {
		t.Fatalf("installCodex: %v", err)
	}
	if result.Files != 1 {
		t.Errorf("expected 1 file, got %d", result.Files)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "[mcp_servers.kektordb]") {
		t.Error("TOML missing [mcp_servers.kektordb] section")
	}
	if !strings.Contains(content, "command =") {
		t.Error("TOML missing command")
	}
	if !strings.Contains(content, "--mcp") {
		t.Error("TOML missing --mcp flag")
	}
	if !strings.Contains(content, "--tools=agent") {
		t.Error("TOML missing --tools=agent flag")
	}
	_ = configPath // used
}

// ─── Claude Code allowlist ───────────────────────────────────────────────────

func TestClaudeCodeAllowlistFormat(t *testing.T) {
	// Test that the allowlist uses the correct "mcp__kektordb__" prefix.
	tmpDir := t.TempDir()
	origHome := userHomeDir
	userHomeDir = func() (string, error) { return tmpDir, nil }
	defer func() { userHomeDir = origHome }()

	if err := addClaudeCodeAllowlist(); err != nil {
		t.Fatalf("addClaudeCodeAllowlist: %v", err)
	}

	settingsPath := claudeSettingsPath()
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}

	var permissions map[string]json.RawMessage
	if err := json.Unmarshal(settings["permissions"], &permissions); err != nil {
		t.Fatalf("parse permissions: %v", err)
	}

	var allow []string
	if err := json.Unmarshal(permissions["allow"], &allow); err != nil {
		t.Fatalf("parse allow: %v", err)
	}

	if len(allow) == 0 {
		t.Fatal("allow list is empty")
	}

	for _, entry := range allow {
		if !strings.HasPrefix(entry, "mcp__kektordb__") {
			t.Errorf("allowlist entry missing prefix: %s", entry)
		}
		toolName := strings.TrimPrefix(entry, "mcp__kektordb__")
		if !mcpi.ProfileAgent[toolName] {
			t.Errorf("allowlist entry is not in ProfileAgent: %s", toolName)
		}
	}
}

// ─── resolveHome ─────────────────────────────────────────────────────────────

func TestResolveHome(t *testing.T) {
	// Save original.
	origHome := userHomeDir
	userHomeDir = func() (string, error) { return "/home/testuser", nil }
	defer func() { userHomeDir = origHome }()

	result := resolveHome("~/.config/test")
	expected := "/home/testuser/.config/test"
	if result != expected {
		t.Errorf("resolveHome = %q, want %q", result, expected)
	}

	// No tilde — return unchanged.
	result = resolveHome("/absolute/path")
	if result != "/absolute/path" {
		t.Errorf("resolveHome = %q, want %q", result, "/absolute/path")
	}
}

// ─── OpenCode embed ──────────────────────────────────────────────────────────

func TestOpenCodePluginEmbedExists(t *testing.T) {
	data, err := openCodeFS.ReadFile(openCodePluginFile)
	if err != nil {
		t.Fatalf("embedded plugin not found: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("embedded plugin is empty")
	}
	content := string(data)
	if !strings.Contains(content, "KEKTORDB_URL") {
		t.Error("plugin missing KEKTORDB_URL constant")
	}
	if !strings.Contains(content, "MEMORY_INSTRUCTIONS") {
		t.Error("plugin missing MEMORY_INSTRUCTIONS")
	}
}

// ─── ResolveTools (from internal/mcp) ────────────────────────────────────────

func TestResolveToolsAll(t *testing.T) {
	allowed := mcpi.ResolveTools("all")
	if allowed != nil {
		t.Error("ResolveTools('all') should return nil")
	}
}

func TestResolveToolsEmpty(t *testing.T) {
	allowed := mcpi.ResolveTools("")
	if allowed != nil {
		t.Error("ResolveTools('') should return nil")
	}
}

func TestResolveToolsAgent(t *testing.T) {
	allowed := mcpi.ResolveTools("agent")
	if len(allowed) == 0 {
		t.Fatal("ResolveTools('agent') returned empty map")
	}
	for tool := range allowed {
		if !mcpi.ProfileAgent[tool] {
			t.Errorf("tool %q is not in ProfileAgent", tool)
		}
	}
	// Agent profile should have at least 14 tools.
	if len(allowed) < 14 {
		t.Errorf("agent profile has %d tools, expected >= 14", len(allowed))
	}
}

func TestResolveToolsAdmin(t *testing.T) {
	allowed := mcpi.ResolveTools("admin")
	if len(allowed) == 0 {
		t.Fatal("ResolveTools('admin') returned empty map")
	}
	for tool := range allowed {
		if !mcpi.ProfileAdmin[tool] {
			t.Errorf("tool %q is not in ProfileAdmin", tool)
		}
	}
}

func TestResolveToolsCombined(t *testing.T) {
	allowed := mcpi.ResolveTools("agent,admin")
	if len(allowed) == 0 {
		t.Fatal("ResolveTools('agent,admin') returned empty map")
	}
	// Should have tools from both profiles.
	hasAgent := false
	hasAdmin := false
	for tool := range allowed {
		if mcpi.ProfileAgent[tool] {
			hasAgent = true
		}
		if mcpi.ProfileAdmin[tool] {
			hasAdmin = true
		}
	}
	if !hasAgent || !hasAdmin {
		t.Error("combined profile missing tools from one or both profiles")
	}
}

func TestResolveToolsIndividualTool(t *testing.T) {
	allowed := mcpi.ResolveTools("save_memory")
	if len(allowed) != 1 || !allowed["save_memory"] {
		t.Errorf("ResolveTools('save_memory') = %v, want {save_memory: true}", allowed)
	}
}
