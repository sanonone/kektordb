package cognitive

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/client"
)

// getTestClient creates a client for testing.
// Set KEKTOR_TEST_HOST and KEKTOR_TEST_PORT to use a specific server,
// otherwise defaults to localhost:9091.
func getTestClient(t *testing.T) *client.Client {
	host := os.Getenv("KEKTOR_TEST_HOST")
	if host == "" {
		host = "localhost"
	}
	port := 9091
	if p := os.Getenv("KEKTOR_TEST_PORT"); p != "" {
		// Simple atoi, ignoring errors for brevity
		if len(p) > 0 {
			port = int(p[0]-'0')*1000 + int(p[1]-'0')*100 + int(p[2]-'0')*10 + int(p[3]-'0')
		}
	}

	c := client.New(host, port, "")

	// Test connection
	if _, err := c.ListIndexes(); err != nil {
		t.Skipf("KektorDB server not available at %s:%d: %v", host, port, err)
	}

	return c
}

func TestSessionManager_CreateSession(t *testing.T) {
	c := getTestClient(t)
	sm := NewSessionManager(c)

	opts := SessionOptions{
		UserID: "test-user",
		Metadata: map[string]interface{}{
			"test": true,
		},
	}

	session, err := sm.CreateSession(opts)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	if session.ID == "" {
		t.Error("Expected session ID to be set")
	}

	// Cleanup
	if err := sm.EndSession(session.ID); err != nil {
		t.Errorf("Failed to end session: %v", err)
	}
}

func TestSessionManager_GetSession(t *testing.T) {
	c := getTestClient(t)
	sm := NewSessionManager(c)

	session, err := sm.CreateSession(SessionOptions{})
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	retrieved, err := sm.GetSession(session.ID)
	if err != nil {
		t.Fatalf("Failed to get session: %v", err)
	}

	if retrieved.ID != session.ID {
		t.Errorf("Expected session ID %s, got %s", session.ID, retrieved.ID)
	}

	// Cleanup
	sm.EndSession(session.ID)
}

func TestSessionManager_ListSessions(t *testing.T) {
	c := getTestClient(t)
	sm := NewSessionManager(c)

	initialCount := len(sm.ListSessions())

	session1, _ := sm.CreateSession(SessionOptions{})
	session2, _ := sm.CreateSession(SessionOptions{})

	sessions := sm.ListSessions()
	if len(sessions) != initialCount+2 {
		t.Errorf("Expected %d sessions, got %d", initialCount+2, len(sessions))
	}

	// Cleanup
	sm.EndSession(session1.ID)
	sm.EndSession(session2.ID)
}

func TestSession_AddMessage(t *testing.T) {
	c := getTestClient(t)
	sm := NewSessionManager(c)

	session, err := sm.CreateSession(SessionOptions{})
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}
	defer sm.EndSession(session.ID)

	session.AddMessage("user", "Hello")
	session.AddMessage("assistant", "Hi there!")

	ctx := session.GetContext()
	if len(ctx) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(ctx))
	}

	if ctx[0].Role != "user" || ctx[0].Content != "Hello" {
		t.Errorf("First message mismatch: %+v", ctx[0])
	}
}

func TestWithSession(t *testing.T) {
	c := getTestClient(t)
	sm := NewSessionManager(c)

	var sessionID string
	err := WithSession(sm, SessionOptions{UserID: "test"}, func(session *ManagedSession) error {
		sessionID = session.ID
		session.AddMessage("user", "Test message")
		return nil
	})

	if err != nil {
		t.Fatalf("WithSession failed: %v", err)
	}

	// Session should be ended after WithSession returns
	_, err = sm.GetSession(sessionID)
	if err == nil {
		t.Error("Expected session to be ended after WithSession")
	}
}

func TestWithSessionContext(t *testing.T) {
	c := getTestClient(t)
	sm := NewSessionManager(c)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := WithSessionContext(ctx, sm, SessionOptions{}, func(session *ManagedSession) error {
		session.AddMessage("user", "Test")
		return nil
	})

	if err != nil {
		t.Fatalf("WithSessionContext failed: %v", err)
	}
}

func TestContextAssembler_Retrieve(t *testing.T) {
	c := getTestClient(t)

	// This test requires a configured pipeline
	pipelineName := os.Getenv("KEKTOR_TEST_PIPELINE")
	if pipelineName == "" {
		t.Skip("Skipping: KEKTOR_TEST_PIPELINE not set")
	}

	ca := NewContextAssembler(c, nil)

	resp, err := ca.Retrieve(pipelineName, "test query", 5)
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}

	if resp.TotalTokens < 0 {
		t.Error("Expected non-negative token count")
	}
}

func TestContextAssembler_RetrieveWithContext(t *testing.T) {
	c := getTestClient(t)
	sm := NewSessionManager(c)

	pipelineName := os.Getenv("KEKTOR_TEST_PIPELINE")
	if pipelineName == "" {
		t.Skip("Skipping: KEKTOR_TEST_PIPELINE not set")
	}

	session, err := sm.CreateSession(SessionOptions{})
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}
	defer sm.EndSession(session.ID)

	ca := NewContextAssembler(c, nil)

	resp, err := ca.RetrieveWithContext(session, pipelineName, "test query", 5)
	if err != nil {
		t.Fatalf("RetrieveWithContext failed: %v", err)
	}

	// Check that message was added to session
	ctx := session.GetContext()
	if len(ctx) < 2 {
		t.Errorf("Expected at least 2 messages in session context, got %d", len(ctx))
	}

	_ = resp
}

func TestFormatSources(t *testing.T) {
	sources := []client.SourceAttribution{
		{
			ChunkID:    "chunk-1",
			Filename:   "test.pdf",
			Relevance:  0.95,
			GraphDepth: 0,
			GraphPath: client.GraphPath{
				Formatted: "doc-1 -> chunk-1",
			},
			Content: "This is test content",
		},
	}

	formatted := FormatSources(sources)
	if formatted == "" {
		t.Error("Expected non-empty formatted output")
	}
}

func TestFilterSources(t *testing.T) {
	sources := []client.SourceAttribution{
		{ChunkID: "1", Relevance: 0.9, Verified: true},
		{ChunkID: "2", Relevance: 0.5, Verified: false},
		{ChunkID: "3", Relevance: 0.95, Verified: true, GraphDepth: 3},
	}

	// Filter by relevance
	filtered := FilterSources(sources, SourceFilter{MinRelevance: 0.8})
	if len(filtered) != 2 {
		t.Errorf("Expected 2 sources with relevance >= 0.8, got %d", len(filtered))
	}

	// Filter by verification
	filtered = FilterSources(sources, SourceFilter{VerifiedOnly: true})
	if len(filtered) != 2 {
		t.Errorf("Expected 2 verified sources, got %d", len(filtered))
	}

	// Filter by depth
	filtered = FilterSources(sources, SourceFilter{MaxDepth: 2})
	if len(filtered) != 2 {
		t.Errorf("Expected 2 sources with depth <= 2, got %d", len(filtered))
	}
}

func TestGroupSourcesByDocument(t *testing.T) {
	sources := []client.SourceAttribution{
		{ChunkID: "c1", DocumentID: "doc-a"},
		{ChunkID: "c2", DocumentID: "doc-a"},
		{ChunkID: "c3", DocumentID: "doc-b"},
		{ChunkID: "c4", DocumentID: ""},
	}

	groups := GroupSourcesByDocument(sources)

	if len(groups["doc-a"]) != 2 {
		t.Errorf("Expected 2 sources in doc-a, got %d", len(groups["doc-a"]))
	}

	if len(groups["doc-b"]) != 1 {
		t.Errorf("Expected 1 source in doc-b, got %d", len(groups["doc-b"]))
	}

	if len(groups["unknown"]) != 1 {
		t.Errorf("Expected 1 source in unknown (empty doc ID), got %d", len(groups["unknown"]))
	}
}

func TestMultiAgentCoordinator_RegisterAgent(t *testing.T) {
	c := getTestClient(t)
	coordinator := NewMultiAgentCoordinator(c)
	defer coordinator.Cleanup()

	agent, err := coordinator.RegisterAgent(AgentConfig{
		ID:           "test-agent",
		Name:         "Test Agent",
		Role:         AgentRoleRetriever,
		PipelineName: "",
	})

	if err != nil {
		t.Fatalf("Failed to register agent: %v", err)
	}

	if agent.Config.ID != "test-agent" {
		t.Errorf("Expected agent ID 'test-agent', got %s", agent.Config.ID)
	}

	if agent.session == nil {
		t.Error("Expected agent to have a session")
	}
}

func TestMultiAgentCoordinator_ExecuteAgent(t *testing.T) {
	c := getTestClient(t)
	coordinator := NewMultiAgentCoordinator(c)
	defer coordinator.Cleanup()

	agent, err := coordinator.RegisterAgent(AgentConfig{
		ID:   "test-agent",
		Name: "Test Agent",
		Role: AgentRoleAnalyzer,
	})

	if err != nil {
		t.Fatalf("Failed to register agent: %v", err)
	}

	result, err := coordinator.ExecuteAgent(agent.Config.ID, "test query")
	if err != nil {
		t.Fatalf("ExecuteAgent failed: %v", err)
	}

	if result.AgentID != agent.Config.ID {
		t.Errorf("Expected result from agent %s, got %s", agent.Config.ID, result.AgentID)
	}

	if !result.Success {
		t.Errorf("Expected successful execution, got error: %v", result.Error)
	}
}

func TestMultiAgentCoordinator_ParallelExecution(t *testing.T) {
	c := getTestClient(t)
	coordinator := NewMultiAgentCoordinator(c)
	defer coordinator.Cleanup()

	// Register multiple agents
	for i := 0; i < 3; i++ {
		_, err := coordinator.RegisterAgent(AgentConfig{
			ID:   fmt.Sprintf("agent-%d", i),
			Name: fmt.Sprintf("Agent %d", i),
			Role: AgentRoleRetriever,
		})
		if err != nil {
			t.Fatalf("Failed to register agent %d: %v", i, err)
		}
	}

	agentIDs := []string{"agent-0", "agent-1", "agent-2"}
	results, err := coordinator.ExecuteParallel(agentIDs, "parallel test query")

	if err != nil {
		t.Fatalf("Parallel execution failed: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(results))
	}
}

func TestMultiAgentCoordinator_SharedState(t *testing.T) {
	c := getTestClient(t)
	coordinator := NewMultiAgentCoordinator(c)
	defer coordinator.Cleanup()

	coordinator.SetSharedState("key1", "value1")

	val, exists := coordinator.GetSharedState("key1")
	if !exists {
		t.Error("Expected key1 to exist in shared state")
	}

	if val != "value1" {
		t.Errorf("Expected 'value1', got %v", val)
	}

	_, exists = coordinator.GetSharedState("nonexistent")
	if exists {
		t.Error("Expected nonexistent key to not exist")
	}
}

func TestWithCoordinator(t *testing.T) {
	c := getTestClient(t)

	err := WithCoordinator(c, func(coordinator *MultiAgentCoordinator) error {
		_, err := coordinator.RegisterAgent(AgentConfig{
			ID:   "temp-agent",
			Name: "Temporary Agent",
			Role: AgentRolePlanner,
		})
		return err
	})

	if err != nil {
		t.Fatalf("WithCoordinator failed: %v", err)
	}
}
