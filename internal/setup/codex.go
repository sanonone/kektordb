package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// codexConfigPath returns the path to Codex's config.toml.
func codexConfigPath() string {
	home, _ := userHomeDir()
	return filepath.Join(home, ".codex", "config.toml")
}

func installCodex() (*Result, error) {
	path := codexConfigPath()

	// Read existing content.
	var data []byte
	existing, err := readFileFn(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read config: %w", err)
		}
		// File doesn't exist — start empty.
	} else {
		data = existing
	}

	// Idempotent: skip if already present.
	if strings.Contains(string(data), "[mcp_servers.kektordb]") {
		return &Result{
			Agent:       "codex",
			Destination: path,
			Files:       0,
		}, nil
	}

	exe := resolveKektordbCommand()
	block := fmt.Sprintf(
		"\n[mcp_servers.kektordb]\ncommand = %q\nargs = [\"--mcp\", \"--tools=agent\"]\n",
		exe,
	)

	// Ensure the file ends with a newline before appending.
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	data = append(data, []byte(block)...)

	if err := ensureConfigDir(path); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	if err := writeFileFn(path, data, 0644); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}

	return &Result{
		Agent:       "codex",
		Destination: path,
		Files:       1,
	}, nil
}
