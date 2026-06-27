// Package mcp — MCP prompt handlers (on-demand, named prompts exposed to clients
// that implement the `prompts/list` + `prompts/get` protocol).
//
// Currently registers a single prompt, `memory_instructions`, that mirrors the
// content of skills/kektordb/SKILL.md (the user-facing markdown file shipped in
// the repo root for clients that support skill files). See
// TestMemoryInstructionsInSync for the parity guarantee.
package mcp

import (
	"context"
	_ "embed"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Prompt name constants — used by MCP server registration and clients.
const (
	// PromptMemoryInstructions is the prompt exposed via the MCP `prompts`
	// capability. Clients surface it as a slash-command / prompt picker
	// entry. It returns the same memory protocol instructions shipped in
	// skills/kektordb/SKILL.md.
	//
	// NOTE: MCP prompts are on-demand — the client/user must explicitly
	// invoke them. This is complementary to the OpenCode TS plugin's
	// `systemPrompt` hook (which auto-injects) and to manual copy of
	// skills/kektordb/SKILL.md into the agent's skill file.
	PromptMemoryInstructions = "memory_instructions"
)

// roleUser is the only valid role for PromptMessage.Content besides
// "assistant" (per the MCP spec — "system" is NOT a valid role). We use
// "user" so the message is treated as a user-side instruction that the
// model then acts upon; this matches the SDK's own example
// (go-sdk@v1.2.0/mcp/server_example_test.go:27).
const roleUser mcp.Role = "user"

// memoryInstructions is the prompt body exposed via PromptMemoryInstructions.
// Source-of-truth is the file itself (//go:embed below). A mirror copy of
// this file lives at skills/kektordb/SKILL.md in the repo root, so users
// can copy it into agent systems that support skill files (Hermes,
// Claude Code, etc.). TestMemoryInstructionsInSync enforces parity.
//
//go:embed memory_instructions.md
var memoryInstructions string

// GetMemoryInstructionsPrompt is the MCP prompt handler for the
// "memory_instructions" prompt. It returns a single user-role message
// containing the memory protocol instructions. The handler is static
// (no arguments, no service state) — it always returns the same content.
func (s *Service) GetMemoryInstructionsPrompt(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	return &mcp.GetPromptResult{
		Description: "KektorDB memory protocol — when and how to save/recall memories.",
		Messages: []*mcp.PromptMessage{
			{
				Role:    roleUser,
				Content: &mcp.TextContent{Text: memoryInstructions},
			},
		},
	}, nil
}
