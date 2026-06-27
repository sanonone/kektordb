package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestGetMemoryInstructionsPrompt verifies the prompt handler returns a
// well-formed GetPromptResult: 1 message, role=user, TextContent, with the
// expected sections of the memory protocol in the body.
func TestGetMemoryInstructionsPrompt(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)

	res, err := svc.GetMemoryInstructionsPrompt(context.Background(), &mcp.GetPromptRequest{})
	if err != nil {
		t.Fatalf("GetMemoryInstructionsPrompt failed: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if len(res.Messages) != 1 {
		t.Fatalf("expected exactly 1 message, got %d", len(res.Messages))
	}
	msg := res.Messages[0]
	if msg.Role != "user" {
		t.Errorf("expected role 'user', got %q", msg.Role)
	}
	tc, ok := msg.Content.(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", msg.Content)
	}
	body := tc.Text
	// Spot-check the most important sections so future edits to the
	// memory_instructions.md file are caught by tests.
	wantSubstrings := []string{
		"KektorDB Persistent Memory",
		"WHEN TO SAVE",
		"save_memory",
		"layer",
		"tags",
		"session_id",
		"WHEN TO RECALL",
	}
	for _, s := range wantSubstrings {
		if !strings.Contains(body, s) {
			t.Errorf("expected prompt body to contain %q, got: %q", s, body)
		}
	}
	if res.Description == "" {
		t.Error("expected non-empty description")
	}
}

// TestMemoryInstructionsInSync ensures the embedded prompt source-of-truth
// (internal/mcp/memory_instructions.md) stays byte-identical to the
// user-facing mirror at the repo root (skills/kektordb/SKILL.md). Drift
// between the two files breaks manual copy-paste workflows, so we fail
// the build rather than silently diverge.
//
// To re-sync after intentional edits, run `make sync-skills` (the
// Makefile target defined alongside this test).
func TestMemoryInstructionsInSync(t *testing.T) {
	// Repo root is the parent of internal/mcp/, where this test lives.
	repoRoot := filepath.Join("..", "..")
	mirror := filepath.Join(repoRoot, "skills", "kektordb", "SKILL.md")

	want, err := os.ReadFile(mirror)
	if err != nil {
		t.Fatalf("read mirror %s: %v (run `make sync-skills` to bootstrap it)", mirror, err)
	}
	if string(want) != memoryInstructions {
		t.Errorf("drift between embedded source and %s\n"+
			"  embedded length: %d bytes\n"+
			"  mirror length:   %d bytes\n"+
			"Run `make sync-skills` and re-commit.",
			mirror, len(memoryInstructions), len(want))
	}
}

// TestPromptMemoryInstructionsConstant checks the exported name constant
// so callers (clients wiring the prompt) and tests can rely on the
// well-known string.
func TestPromptMemoryInstructionsConstant(t *testing.T) {
	if PromptMemoryInstructions != "memory_instructions" {
		t.Errorf("PromptMemoryInstructions = %q, want %q",
			PromptMemoryInstructions, "memory_instructions")
	}
}
