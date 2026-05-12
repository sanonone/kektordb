package setup

import (
	"encoding/json"
	"fmt"
	"os"
)

// injectMCPEntry reads a JSON config file at configPath, ensures parentKey contains
// an entry for serverName with the given entry data, and writes it back.
// If the file does not exist, it is created.
// If the server is already registered, it is skipped (idempotent).
// entry should be a map with "command" and "args" keys.
func injectMCPEntry(configPath, parentKey, serverName string, entry map[string]any) error {
	var config map[string]json.RawMessage
	data, err := readFileFn(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			config = make(map[string]json.RawMessage)
		} else {
			return fmt.Errorf("read config: %w", err)
		}
	} else if len(data) > 0 {
		if err := json.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("parse config: %w", err)
		}
	} else {
		config = make(map[string]json.RawMessage)
	}

	// Parse or create the parent block (e.g. "mcp", "mcpServers", "mcp_servers").
	var parent map[string]json.RawMessage
	if raw, exists := config[parentKey]; exists {
		if err := json.Unmarshal(raw, &parent); err != nil {
			return fmt.Errorf("parse %s block: %w", parentKey, err)
		}
	} else {
		parent = make(map[string]json.RawMessage)
	}

	// Idempotent: skip if already registered.
	if _, exists := parent[serverName]; exists {
		return nil
	}

	entryJSON, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal entry: %w", err)
	}
	parent[serverName] = json.RawMessage(entryJSON)

	parentJSON, err := json.Marshal(parent)
	if err != nil {
		return fmt.Errorf("marshal parent: %w", err)
	}
	config[parentKey] = json.RawMessage(parentJSON)

	if err := ensureConfigDir(configPath); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	output, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	output = append(output, '\n')

	if err := writeFileFn(configPath, output, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// standardMCPEntry returns the standard MCP entry for kektordb.
// It includes the absolute binary path and --mcp --tools=agent flags.
func standardMCPEntry() map[string]any {
	return map[string]any{
		"command": resolveKektordbCommand(),
		"args":    []string{"--mcp", "--tools=agent"},
	}
}
