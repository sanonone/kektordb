package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	mcpi "github.com/sanonone/kektordb/internal/mcp"
)

// claudeCodeDir returns the Claude Code config directory.
func claudeCodeDir() string {
	home, _ := userHomeDir()
	return filepath.Join(home, ".claude")
}

// claudeCodeMCPDir returns the MCP config directory for Claude Code.
func claudeCodeMCPDir() string {
	return filepath.Join(claudeCodeDir(), "mcp")
}

// claudeCodeMCPPath returns the path to the kektordb MCP config for Claude Code.
func claudeCodeMCPPath() string {
	return filepath.Join(claudeCodeMCPDir(), "kektordb.json")
}

// claudeSettingsPath returns the path to Claude Code settings.json.
func claudeSettingsPath() string {
	return filepath.Join(claudeCodeDir(), "settings.json")
}

func installClaudeCode() (*Result, error) {
	// Step 1: Check if Claude CLI is available (best-effort, warn if not).
	claudeBin, err := lookPathFn("claude")
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: claude CLI not found in PATH — install Claude Code first\n")
		fmt.Fprintf(os.Stderr, "  The MCP config will still be written, but you'll need to install Claude manually.\n")
	} else {
		// Step 2: Add marketplace (best-effort, warn if not).
		addOut, addErr := runCommand(claudeBin, "plugin", "marketplace", "add", "sanonone/kektordb")
		if addErr != nil && !strings.Contains(string(addOut), "already") {
			fmt.Fprintf(os.Stderr, "warning: marketplace add failed: %s\n", strings.TrimSpace(string(addOut)))
		}

		// Step 3: Install plugin (best-effort, warn if not).
		installOut, installErr := runCommand(claudeBin, "plugin", "install", "kektordb")
		if installErr != nil && !strings.Contains(string(installOut), "already") {
			fmt.Fprintf(os.Stderr, "warning: plugin install failed: %s\n", strings.TrimSpace(string(installOut)))
		}
		_ = addOut    // used only in error path
		_ = installOut // used only in error path
	}

	// Step 4: Write MCP config with absolute binary path.
	files := 0
	if err := writeClaudeCodeMCP(); err != nil {
		return nil, fmt.Errorf("write MCP config: %w", err)
	}
	files++

	// Step 5: Add allowlist to settings.json (best-effort).
	if err := addClaudeCodeAllowlist(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not add allowlist: %v\n", err)
	} else {
		files++
	}

	return &Result{
		Agent:       "claude-code",
		Destination: claudeCodeMCPPath(),
		Files:       files,
	}, nil
}

// writeClaudeCodeMCP writes ~/.claude/mcp/kektordb.json with the absolute binary path.
func writeClaudeCodeMCP() error {
	exe := resolveKektordbCommand()
	entry := map[string]any{
		"command": exe,
		"args":    []string{"--mcp", "--tools=agent"},
	}

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	data = append(data, '\n')

	dir := claudeCodeMCPDir()
	if err := mkdirAllFn(dir, 0755); err != nil {
		return fmt.Errorf("create mcp dir: %w", err)
	}

	if err := writeFileFn(claudeCodeMCPPath(), data, 0644); err != nil {
		return fmt.Errorf("write MCP config: %w", err)
	}
	return nil
}

// addClaudeCodeAllowlist adds KektorDB tool permissions to Claude Code's settings.json.
// Format: "mcp__kektordb__<tool_name>" for each agent-profile tool.
func addClaudeCodeAllowlist() error {
	path := claudeSettingsPath()

	var settings map[string]json.RawMessage
	data, err := readFileFn(path)
	if err != nil {
		if os.IsNotExist(err) {
			settings = make(map[string]json.RawMessage)
		} else {
			return fmt.Errorf("read settings: %w", err)
		}
	} else if len(data) > 0 {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("parse settings: %w", err)
		}
	} else {
		settings = make(map[string]json.RawMessage)
	}

	// Parse or create permissions block.
	var permissions map[string]json.RawMessage
	if raw, exists := settings["permissions"]; exists {
		if err := json.Unmarshal(raw, &permissions); err != nil {
			return fmt.Errorf("parse permissions: %w", err)
		}
	} else {
		permissions = make(map[string]json.RawMessage)
	}

	// Parse or create allow list.
	var allow []string
	if raw, exists := permissions["allow"]; exists {
		if err := json.Unmarshal(raw, &allow); err != nil {
			return fmt.Errorf("parse allow: %w", err)
		}
	}

	// Build the set of existing allow entries.
	existing := make(map[string]bool)
	for _, a := range allow {
		existing[a] = true
	}

	// Add each agent-profile tool that's not already present.
	added := 0
	for toolName := range mcpi.ProfileAgent {
		entry := "mcp__kektordb__" + toolName
		if !existing[entry] {
			allow = append(allow, entry)
			added++
		}
	}
	if added == 0 {
		return nil // all already present
	}

	allowJSON, err := json.Marshal(allow)
	if err != nil {
		return fmt.Errorf("marshal allow: %w", err)
	}
	permissions["allow"] = json.RawMessage(allowJSON)

	permJSON, err := json.Marshal(permissions)
	if err != nil {
		return fmt.Errorf("marshal permissions: %w", err)
	}
	settings["permissions"] = json.RawMessage(permJSON)

	if err := ensureConfigDir(path); err != nil {
		return fmt.Errorf("create settings dir: %w", err)
	}

	output, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	output = append(output, '\n')

	if err := writeFileFn(path, output, 0644); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}
