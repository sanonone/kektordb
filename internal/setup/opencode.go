package setup

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed plugins/opencode/*
var openCodeFS embed.FS

const openCodePluginFile = "plugins/opencode/kektordb.ts"

// openCodeConfigDir returns the OpenCode config directory.
func openCodeConfigDir() string {
	home, _ := userHomeDir()
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "opencode")
	}
	return filepath.Join(home, ".config", "opencode")
}

// openCodePluginDir returns the directory where OpenCode plugins are stored.
func openCodePluginDir() string {
	return filepath.Join(openCodeConfigDir(), "plugins")
}

// openCodeConfigPath returns the path to opencode.json or opencode.jsonc.
func openCodeConfigPath() string {
	dir := openCodeConfigDir()
	jsonc := filepath.Join(dir, "opencode.jsonc")
	if _, err := statFn(jsonc); err == nil {
		return jsonc
	}
	return filepath.Join(dir, "opencode.json")
}

func installOpenCode() (*Result, error) {
	dir := openCodePluginDir()
	if err := mkdirAllFn(dir, 0755); err != nil {
		return nil, fmt.Errorf("create plugin dir: %w", err)
	}

	// Read the embedded TypeScript plugin.
	data, err := openCodeFS.ReadFile(openCodePluginFile)
	if err != nil {
		return nil, fmt.Errorf("read embedded plugin: %w", err)
	}

	// Patch the KEKTORDB_BIN constant with the absolute binary path.
	data = patchKektordbBINLine(data, resolveKektordbCommand())

	// Write the plugin file.
	dest := filepath.Join(dir, "kektordb.ts")
	if err := writeFileFn(dest, data, 0644); err != nil {
		return nil, fmt.Errorf("write plugin: %w", err)
	}

	files := 1

	// Inject MCP registration in opencode.json.
	if err := injectOpenCodeMCP(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not auto-register MCP in opencode.json: %v\n", err)
		fmt.Fprintf(os.Stderr, "  Add manually to opencode.json MCP section:\n")
		fmt.Fprintf(os.Stderr, "  %q, \"--mcp\", \"--tools=agent\"\n", resolveKektordbCommand())
	} else {
		files++
	}

	return &Result{
		Agent:       "opencode",
		Destination: dir,
		Files:       files,
	}, nil
}

// patchKektordbBINLine replaces the KEKTORDB_BIN constant in the TypeScript plugin
// with the absolute binary path.
func patchKektordbBINLine(src []byte, absBin string) []byte {
	const marker = `const KEKTORDB_BIN = process.env.KEKTORDB_BIN ?? "kektordb"`

	var replacement string
	if absBin == "kektordb" {
		replacement = `const KEKTORDB_BIN = process.env.KEKTORDB_BIN ?? "kektordb"`
	} else {
		replacement = fmt.Sprintf(`const KEKTORDB_BIN = process.env.KEKTORDB_BIN ?? %q`, absBin)
	}

	return []byte(strings.Replace(string(src), marker, replacement, 1))
}

// injectOpenCodeMCP adds the KektorDB MCP entry to opencode.json.
func injectOpenCodeMCP() error {
	path := openCodeConfigPath()

	// OpenCode uses a different MCP format: command is an array of strings.
	entry := map[string]any{
		"type":    "local",
		"command": []string{resolveKektordbCommand(), "--mcp", "--tools=agent"},
		"enabled": true,
	}

	if err := injectMCPEntry(path, "mcp", "kektordb", entry); err != nil {
		return fmt.Errorf("inject MCP: %w", err)
	}
	return nil
}
