package cognitive

import (
	"context"
	"fmt"
	"sync"

	"github.com/sanonone/kektordb/pkg/client"
)

// AgentRole defines the role of an agent in a multi-agent system.
type AgentRole string

const (
	// AgentRolePlanner creates execution plans.
	AgentRolePlanner AgentRole = "planner"
	// AgentRoleRetriever retrieves information.
	AgentRoleRetriever AgentRole = "retriever"
	// AgentRoleAnalyzer analyzes and synthesizes information.
	AgentRoleAnalyzer AgentRole = "analyzer"
	// AgentRoleValidator validates outputs.
	AgentRoleValidator AgentRole = "validator"
)

// AgentConfig configures an individual agent.
type AgentConfig struct {
	ID            string
	Name          string
	Role          AgentRole
	PipelineName  string
	Instructions  string
	MaxIterations int
}

// Agent represents a single agent in the coordinator.
type Agent struct {
	Config      AgentConfig
	session     *Session
	coordinator *MultiAgentCoordinator
}

// AgentResult represents the result of an agent's execution.
type AgentResult struct {
	AgentID  string
	Success  bool
	Output   string
	Sources  []client.SourceAttribution
	Error    error
	Metadata map[string]interface{}
}

// MultiAgentCoordinator orchestrates multiple agents for complex tasks.
type MultiAgentCoordinator struct {
	client      *client.Client
	indexName   string
	agents      map[string]*Agent
	agentOrder  []string
	sessionMgr  *SessionManager
	assembler   *ContextAssembler
	mu          sync.RWMutex
	sharedState map[string]interface{}
}

// NewMultiAgentCoordinator creates a new coordinator.
func NewMultiAgentCoordinator(c *client.Client, indexName string) *MultiAgentCoordinator {
	return &MultiAgentCoordinator{
		client:      c,
		indexName:   indexName,
		agents:      make(map[string]*Agent),
		agentOrder:  []string{},
		sessionMgr:  NewSessionManager(c, indexName),
		sharedState: make(map[string]interface{}),
	}
}

// RegisterAgent adds an agent to the coordinator.
func (mac *MultiAgentCoordinator) RegisterAgent(config AgentConfig) (*Agent, error) {
	mac.mu.Lock()
	defer mac.mu.Unlock()

	if _, exists := mac.agents[config.ID]; exists {
		return nil, fmt.Errorf("agent %s already registered", config.ID)
	}

	// Create a dedicated session for this agent
	opts := SessionOptions{
		Metadata: map[string]interface{}{
			"agent_id":   config.ID,
			"agent_name": config.Name,
			"agent_role": config.Role,
		},
	}

	session, err := mac.sessionMgr.CreateSession(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create session for agent %s: %w", config.ID, err)
	}

	agent := &Agent{
		Config:      config,
		session:     session,
		coordinator: mac,
	}

	mac.agents[config.ID] = agent
	mac.agentOrder = append(mac.agentOrder, config.ID)

	return agent, nil
}

// ExecuteAgent runs a single agent with a query.
func (mac *MultiAgentCoordinator) ExecuteAgent(agentID, query string) (*AgentResult, error) {
	mac.mu.RLock()
	agent, exists := mac.agents[agentID]
	mac.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("agent %s not found", agentID)
	}

	// Use context assembler if pipeline is configured
	if agent.Config.PipelineName != "" && mac.assembler != nil {
		resp, err := mac.assembler.RetrieveWithContext(agent.session, agent.Config.PipelineName, query, 10)
		if err != nil {
			return &AgentResult{
				AgentID: agentID,
				Success: false,
				Error:   err,
			}, nil
		}

		return &AgentResult{
			AgentID: agentID,
			Success: true,
			Output:  resp.ContextText,
			Sources: resp.Sources,
			Metadata: map[string]interface{}{
				"chunks_used":  resp.ChunksUsed,
				"total_tokens": resp.TotalTokens,
			},
		}, nil
	}

	// Fallback: just record the query in session
	agent.session.AddMessage("user", query)

	return &AgentResult{
		AgentID: agentID,
		Success: true,
		Output:  query,
	}, nil
}

// ExecutePipeline runs all agents in sequence.
func (mac *MultiAgentCoordinator) ExecutePipeline(initialQuery string) ([]AgentResult, error) {
	mac.mu.RLock()
	order := make([]string, len(mac.agentOrder))
	copy(order, mac.agentOrder)
	mac.mu.RUnlock()

	results := make([]AgentResult, 0, len(order))
	currentInput := initialQuery

	for _, agentID := range order {
		result, err := mac.ExecuteAgent(agentID, currentInput)
		if err != nil {
			return results, err
		}

		results = append(results, *result)

		if !result.Success {
			// Pipeline stops on first failure
			break
		}

		// Pass output to next agent
		currentInput = result.Output
	}

	return results, nil
}

// ExecuteParallel runs multiple agents concurrently.
func (mac *MultiAgentCoordinator) ExecuteParallel(agentIDs []string, query string) ([]AgentResult, error) {
	var wg sync.WaitGroup
	results := make([]AgentResult, len(agentIDs))
	errors := make([]error, len(agentIDs))

	for i, agentID := range agentIDs {
		wg.Add(1)
		go func(index int, id string) {
			defer wg.Done()
			result, err := mac.ExecuteAgent(id, query)
			if err != nil {
				errors[index] = err
				return
			}
			results[index] = *result
		}(i, agentID)
	}

	wg.Wait()

	// Check for errors
	for _, err := range errors {
		if err != nil {
			return results, err
		}
	}

	return results, nil
}

// SetContextAssembler sets the context assembler for the coordinator.
func (mac *MultiAgentCoordinator) SetContextAssembler(assembler *ContextAssembler) {
	mac.assembler = assembler
}

// GetSharedState retrieves a value from shared state.
func (mac *MultiAgentCoordinator) GetSharedState(key string) (interface{}, bool) {
	mac.mu.RLock()
	defer mac.mu.RUnlock()
	val, exists := mac.sharedState[key]
	return val, exists
}

// SetSharedState sets a value in shared state.
func (mac *MultiAgentCoordinator) SetSharedState(key string, value interface{}) {
	mac.mu.Lock()
	defer mac.mu.Unlock()
	mac.sharedState[key] = value
}

// Cleanup ends all agent sessions.
func (mac *MultiAgentCoordinator) Cleanup() error {
	mac.mu.Lock()
	defer mac.mu.Unlock()

	var lastErr error
	for _, agent := range mac.agents {
		if err := mac.sessionMgr.EndSession(agent.session.ID); err != nil {
			lastErr = err
		}
	}

	mac.agents = make(map[string]*Agent)
	mac.agentOrder = []string{}

	return lastErr
}

// Agent methods

// GetSession returns the agent's dedicated session.
func (a *Agent) GetSession() *Session {
	return a.session
}

// Execute runs the agent with the given input.
func (a *Agent) Execute(ctx context.Context, input string) (*AgentResult, error) {
	return a.coordinator.ExecuteAgent(a.Config.ID, input)
}

// AddKnowledge adds information to the agent's context.
func (a *Agent) AddKnowledge(role, content string) {
	a.session.AddMessage(role, content)
}

// WithCoordinator creates a coordinator, executes a function, and cleans up.
func WithCoordinator(c *client.Client, indexName string, fn func(*MultiAgentCoordinator) error) error {
	coordinator := NewMultiAgentCoordinator(c, indexName)
	defer coordinator.Cleanup()
	return fn(coordinator)
}
