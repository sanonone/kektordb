# Module: internal/setup

## Purpose

One-liner MCP configuration installer for AI coding agents. Writes the MCP server config file for the specified agent, pointing to the KektorDB binary with `--tools=agent` (17 tools) to keep agent context lean. Idempotent — safe to run multiple times.

## Usage

```bash
kektordb setup <agent>
```

## Supported Agents

| Agent | Config File | Notes |
|-------|------------|-------|
| `claude-code` | `~/.claude/mcp/kektordb.json` | Also runs `claude plugin marketplace add sanonone/kektordb` and `claude plugin install kektordb` if Claude CLI is in PATH |
| `cursor` | `~/.cursor/mcp.json` | Standard MCP JSON config |
| `gemini-cli` | `~/.gemini/settings.json` | Gemini CLI MCP integration |
| `codex` | `~/.codex/config.toml` | OpenAI Codex TOML config |
| `opencode` | `~/.config/opencode/opencode.json(c)` + `~/.config/opencode/plugins/kektordb.ts` | Embedded TypeScript plugin via `//go:embed` |

## Architecture

**Agent-agnostic interface:** `Install(agentName, embedderMode string) (*Result, error)` dispatches to agent-specific implementation functions. Each agent module handles its own config path, file format, and binary detection.

**Binary path resolution:** Uses `os.Executable()` to determine the absolute path of the currently running KektorDB binary. The MCP config is written with this absolute path so the agent can launch KektorDB regardless of CWD.

**Idempotency:** All installers are idempotent. Running `kektordb setup claude-code` a second time overwrites the existing config with the same content. Safe for CI/CD and onboarding scripts.

**Config format per agent:**
- Claude Code + Cursor: Standard MCP JSON (`{"mcpServers": {"kektordb-memory": {"command": "...", "args": [...]}}}`)
- Gemini CLI: JSON settings with `mcpServers` key
- Codex: TOML `[mcp_servers.kektordb-memory]`
- OpenCode: JSON/JSONC config file + embedded TypeScript plugin

## Cross-Module Dependencies

**Depends on:**
- `os.Executable()` — For absolute binary path resolution.
- `//go:embed` — The OpenCode TypeScript plugin is embedded at compile time.

**Used by:**
- `cmd/kektordb/main.go` — `kektordb setup` subcommand dispatches to `setup.Install()`.
