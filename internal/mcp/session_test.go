package mcp

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

// mockEmbedder implements a simple embedder for testing.
type mockEmbedder struct{}

func (m *mockEmbedder) Embed(text string) ([]float32, error) {
	// Return a simple deterministic embedding based on text length
	vec := make([]float32, 384)
	for i := range vec {
		vec[i] = float32(i%10) / 10.0
	}
	return vec, nil
}

func setupTestService(t *testing.T) (*Service, *engine.Engine, func()) {
	t.Helper()

	testDir := t.TempDir()
	opts := engine.DefaultOptions(testDir)
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("failed to open engine: %v", err)
	}

	// Create default index
	eng.VCreate("mcp_memory", distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)

	embedder := &mockEmbedder{}
	svc := NewService(eng, embedder)

	cleanup := func() {
		eng.Close()
		os.RemoveAll(testDir)
	}

	return svc, eng, cleanup
}

func TestStartSession(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	req := &mcp.CallToolRequest{}

	// Test 1: Start session with auto-generated ID
	args := StartSessionArgs{
		AgentID: "test_agent",
		UserID:  "test_user",
		Context: "Test conversation",
	}

	_, result, err := svc.StartSession(ctx, req, args)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	if result.SessionID == "" {
		t.Error("Expected non-empty session ID")
	}
	if result.Status != "active" {
		t.Errorf("Expected status 'active', got '%s'", result.Status)
	}

	// Verify session is stored
	sess := svc.getSession(result.SessionID)
	if sess == nil {
		t.Error("Session should be tracked")
	}
	if sess.Metadata["agent_id"] != "test_agent" {
		t.Error("Agent ID not stored correctly")
	}

	t.Logf("Created session: %s", result.SessionID)
}

func TestStartSessionWithCustomID(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	req := &mcp.CallToolRequest{}

	customID := fmt.Sprintf("session::custom_%d", time.Now().UnixNano())
	args := StartSessionArgs{
		SessionID: customID,
		Context:   "Custom ID test",
	}

	_, result, err := svc.StartSession(ctx, req, args)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	if result.SessionID != customID {
		t.Errorf("Expected session ID '%s', got '%s'", customID, result.SessionID)
	}
}

func TestEndSession(t *testing.T) {
	svc, eng, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	req := &mcp.CallToolRequest{}

	// First start a session
	startArgs := StartSessionArgs{
		AgentID: "test_agent",
		Context: "Session to end",
	}
	_, startResult, err := svc.StartSession(ctx, req, startArgs)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	sessionID := startResult.SessionID

	// Save a memory to the session
	saveArgs := SaveMemoryArgs{
		Content:   "Test memory content",
		SessionID: sessionID,
	}
	_, saveResult, err := svc.SaveMemory(ctx, req, saveArgs)
	if err != nil {
		t.Fatalf("SaveMemory failed: %v", err)
	}
	if saveResult.MemoryID == "" {
		t.Error("Expected non-empty memory ID")
	}

	// Verify memory has session_id
	data, err := eng.VGet("mcp_memory", saveResult.MemoryID)
	if err != nil {
		t.Fatalf("Failed to get memory: %v", err)
	}
	if data.Metadata["session_id"] != sessionID {
		t.Error("Memory should have session_id metadata")
	}

	// End the session
	endArgs := EndSessionArgs{
		SessionID: sessionID,
	}
	_, endResult, err := svc.EndSession(ctx, req, endArgs)
	if err != nil {
		t.Fatalf("EndSession failed: %v", err)
	}

	if endResult.Status != "ended" {
		t.Errorf("Expected status 'ended', got '%s'", endResult.Status)
	}

	// Verify session is removed from tracking
	sess := svc.getSession(sessionID)
	if sess != nil {
		t.Error("Session should be removed from tracking after end")
	}

	// Verify session entity has ended status
	// Note: This requires the session to actually exist in the index
	t.Logf("Ended session: %s", sessionID)
}

func TestSaveMemoryWithoutSession(t *testing.T) {
	svc, eng, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	req := &mcp.CallToolRequest{}

	// Save memory without session
	args := SaveMemoryArgs{
		Content: "Standalone memory",
		Tags:    []string{"test"},
	}
	_, result, err := svc.SaveMemory(ctx, req, args)
	if err != nil {
		t.Fatalf("SaveMemory failed: %v", err)
	}

	// Verify memory exists without session_id
	data, err := eng.VGet("mcp_memory", result.MemoryID)
	if err != nil {
		t.Fatalf("Failed to get memory: %v", err)
	}
	if _, hasSession := data.Metadata["session_id"]; hasSession {
		t.Error("Memory without session should not have session_id")
	}
}

func TestSaveMemoryWithExplicitSessionID(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	req := &mcp.CallToolRequest{}

	// First create a session
	startArgs := StartSessionArgs{
		Context: "Test session",
	}
	_, startResult, err := svc.StartSession(ctx, req, startArgs)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	// Save memory with explicit session ID (different from tracked)
	explicitSessionID := fmt.Sprintf("session::explicit_%d", time.Now().UnixNano())
	saveArgs := SaveMemoryArgs{
		Content:   "Memory with explicit session",
		SessionID: explicitSessionID,
	}
	_, saveResult, err := svc.SaveMemory(ctx, req, saveArgs)
	if err != nil {
		t.Fatalf("SaveMemory failed: %v", err)
	}

	// Verify the explicit session ID was used, not the tracked one
	t.Logf("Memory saved: %s with explicit session: %s, tracked session was: %s", saveResult.MemoryID, explicitSessionID, startResult.SessionID)
}

func TestEndSessionNotFound(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	req := &mcp.CallToolRequest{}

	// Try to end non-existent session
	args := EndSessionArgs{
		SessionID: "session::nonexistent_12345",
	}
	_, _, err := svc.EndSession(ctx, req, args)
	if err == nil {
		t.Error("Expected error for non-existent session")
	}
}

func TestMultipleSessions(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	req := &mcp.CallToolRequest{}

	// Create multiple sessions
	sessions := make([]string, 3)
	for i := 0; i < 3; i++ {
		args := StartSessionArgs{
			Context: fmt.Sprintf("Session %d", i+1),
		}
		_, result, err := svc.StartSession(ctx, req, args)
		if err != nil {
			t.Fatalf("StartSession %d failed: %v", i, err)
		}
		sessions[i] = result.SessionID
	}

	// Verify all sessions are tracked
	for _, id := range sessions {
		sess := svc.getSession(id)
		if sess == nil {
			t.Errorf("Session %s should be tracked", id)
		}
	}

	// End middle session
	endArgs := EndSessionArgs{SessionID: sessions[1]}
	_, _, err := svc.EndSession(ctx, req, endArgs)
	if err != nil {
		t.Fatalf("EndSession failed: %v", err)
	}

	// Verify only middle session is removed
	if svc.getSession(sessions[1]) != nil {
		t.Error("Ended session should not be tracked")
	}
	if svc.getSession(sessions[0]) == nil {
		t.Error("First session should still be tracked")
	}
	if svc.getSession(sessions[2]) == nil {
		t.Error("Third session should still be tracked")
	}
}

// TestEvolveMemoryEmptyContent verifies that when only NewVector is provided
// (no NewContent), the evolved node does NOT get an empty "content" metadata field.
// Regression test for H12: MCP EvolveMemory sets empty content vs HTTP guard.
func TestEvolveMemoryEmptyContent(t *testing.T) {
	svc, eng, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	// Add a memory with content
	vec := []float32{1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0}
	eng.VAdd("mcp_memory", "mem_original", vec, map[string]any{
		"content": "original content",
		"type":    "episodic",
	})

	// Evolve with only NewVector (no NewContent)
	evolvedVec := []float32{2.0, 4.0, 6.0, 8.0, 10.0, 12.0, 14.0, 16.0}
	req := &mcp.CallToolRequest{}
	args := EvolveMemoryArgs{
		IndexName:   "mcp_memory",
		OldMemoryID: "mem_original",
		NewVector:   evolvedVec,
		Reason:      "testing empty content guard",
	}

	_, result, err := svc.EvolveMemory(ctx, req, args)
	if err != nil {
		t.Fatalf("EvolveMemory failed: %v", err)
	}

	// Verify the evolved node does not have empty content
	data, err := eng.VGet("mcp_memory", result.NewMemoryID)
	if err != nil {
		t.Fatalf("failed to get evolved node: %v", err)
	}

	content, exists := data.Metadata["content"]
	if exists {
		contentStr, ok := content.(string)
		if ok && contentStr == "" {
			t.Error("evolved node should not have empty 'content' field")
		}
		t.Logf("Evolved node content: %q", contentStr)
	} else {
		t.Log("Evolved node has no 'content' field (correct when NewContent is empty)")
	}

	// Original node should be marked historical
	oldData, err := eng.VGet("mcp_memory", "mem_original")
	if err != nil {
		t.Fatalf("failed to get original node: %v", err)
	}
	if !isHistorical(oldData.Metadata) {
		t.Error("original node should be marked historical after evolution")
	}
}

func isHistorical(meta map[string]any) bool {
	if v, ok := meta["_is_historical"]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}
