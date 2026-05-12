package setup

import (
	"fmt"
	"path/filepath"
)

// cursorConfigPath returns the path to Cursor's MCP config.
func cursorConfigPath() string {
	home, _ := userHomeDir()
	return filepath.Join(home, ".cursor", "mcp.json")
}

func installCursor() (*Result, error) {
	path := cursorConfigPath()
	if err := injectMCPEntry(path, "mcpServers", "kektordb", standardMCPEntry()); err != nil {
		return nil, fmt.Errorf("cursor setup: %w", err)
	}
	return &Result{
		Agent:       "cursor",
		Destination: path,
		Files:       1,
	}, nil
}
