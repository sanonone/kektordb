// Package setup manages the kektordb setup <agent> command.
// It configures MCP integration for AI coding agents (Claude Code, Cursor, etc.).
package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Agent describes a supported AI coding agent.
type Agent struct {
	Name        string // Machine name (e.g. "claude-code")
	Description string // Human-readable description
	InstallDir  string // Where the config file will be written
}

// Result is returned by Install() after a successful setup.
type Result struct {
	Agent       string // Agent name
	Destination string // Path to the config file or directory
	Files       int    // Number of files created/modified
}

// Injectable for tests.
var (
	userHomeDir  = os.UserHomeDir
	lookPathFn   = exec.LookPath
	osExecutable = os.Executable
	runCommand   = func(name string, args ...string) ([]byte, error) {
		return exec.Command(name, args...).CombinedOutput()
	}
	writeFileFn = os.WriteFile
	readFileFn  = os.ReadFile
	statFn      = os.Stat
	mkdirAllFn  = os.MkdirAll
)

// SupportedAgents returns the list of agents that kektordb setup supports.
func SupportedAgents() []Agent {
	return []Agent{
		{
			Name:        "claude-code",
			Description: "Claude Code — MCP config via ~/.claude/mcp/",
			InstallDir:  "~/.claude/mcp/kektordb.json",
		},
		{
			Name:        "cursor",
			Description: "Cursor — MCP config in ~/.cursor/mcp.json",
			InstallDir:  "~/.cursor/mcp.json",
		},
		{
			Name:        "gemini-cli",
			Description: "Gemini CLI — MCP registration in settings.json",
			InstallDir:  "~/.gemini/settings.json",
		},
		{
			Name:        "codex",
			Description: "Codex — MCP registration in config.toml",
			InstallDir:  "~/.codex/config.toml",
		},
		{
			Name:        "opencode",
			Description: "OpenCode — Plugin TypeScript with session tracking and MCP",
			InstallDir:  "~/.config/opencode/plugins/",
		},
	}
}

// Install runs the setup for the named agent. Returns a Result or an error.
// embedderMode is passed through to the agent-specific install (e.g. opencode MCP entry).
func Install(agentName, embedderMode string) (*Result, error) {
	switch agentName {
	case "claude-code":
		return installClaudeCode()
	case "cursor":
		return installCursor()
	case "gemini-cli":
		return installGeminiCLI()
	case "codex":
		return installCodex()
	case "opencode":
		return installOpenCode(embedderMode)
	default:
		return nil, fmt.Errorf("unknown agent: %q (supported: claude-code, cursor, gemini-cli, codex, opencode)", agentName)
	}
}

// resolveKektordbCommand returns the absolute path to the kektordb binary.
// This is CRITICAL: MCP subprocesses do not inherit PATH.
// Falls back to "kektordb" if the executable path cannot be determined.
func resolveKektordbCommand() string {
	exe, err := osExecutable()
	if err != nil {
		return "kektordb"
	}
	// Resolve symlinks (e.g., Homebrew creates symlinks).
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe
}

// resolveHome replaces ~ with the user's home directory.
func resolveHome(path string) string {
	if len(path) > 0 && path[0] == '~' {
		home, err := userHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[1:])
	}
	return path
}

// configDir returns the directory part of a config path, creating it if needed.
func ensureConfigDir(configPath string) error {
	dir := filepath.Dir(configPath)
	return mkdirAllFn(dir, 0755)
}

// isWindows returns true on Windows.
func isWindows() bool {
	return runtime.GOOS == "windows"
}
