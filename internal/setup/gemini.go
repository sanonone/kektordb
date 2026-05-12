package setup

import (
	"fmt"
	"path/filepath"
)

// geminiConfigPath returns the path to Gemini CLI's settings.json.
func geminiConfigPath() string {
	home, _ := userHomeDir()
	return filepath.Join(home, ".gemini", "settings.json")
}

func installGeminiCLI() (*Result, error) {
	path := geminiConfigPath()
	if err := injectMCPEntry(path, "mcp_servers", "kektordb", standardMCPEntry()); err != nil {
		return nil, fmt.Errorf("gemini-cli setup: %w", err)
	}
	return &Result{
		Agent:       "gemini-cli",
		Destination: path,
		Files:       1,
	}, nil
}
