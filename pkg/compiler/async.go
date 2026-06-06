package compiler

import (
	"fmt"
	"sync"
	"time"
)

// compileTask represents an asynchronous compilation operation.
type compileTask struct {
	ID      string        `json:"id"`
	Request CompileRequest `json:"request"`
	Status  CompileStatus  `json:"status"`
	Result  *Artifact      `json:"result,omitempty"`
	Error   string         `json:"error,omitempty"`

	StartedAt time.Time  `json:"started_at"`
	DoneAt    *time.Time `json:"done_at,omitempty"`

	mu sync.RWMutex
}

// compileTaskManager tracks in-flight and completed async compilations.
type compileTaskManager struct {
	tasks map[string]*compileTask
	mu    sync.RWMutex
}

func newCompileTaskManager() *compileTaskManager {
	return &compileTaskManager{
		tasks: make(map[string]*compileTask),
	}
}

// StartAsyncCompile begins an asynchronous compilation and returns a task ID
// for polling. The caller should use GetTaskStatus to check progress.
func (c *Compiler) StartAsyncCompile(req CompileRequest) (string, error) {
	task := &compileTask{
		ID:        generateTaskID(),
		Request:   req,
		Status:    CompileStatusPending,
		StartedAt: time.Now(),
	}

	c.taskManager.mu.Lock()
	c.taskManager.tasks[task.ID] = task
	c.taskManager.mu.Unlock()

	go c.runAsyncCompile(task)

	return task.ID, nil
}

// runAsyncCompile executes the compilation in a background goroutine.
func (c *Compiler) runAsyncCompile(task *compileTask) {
	task.mu.Lock()
	task.Status = CompileStatusCompiling
	task.mu.Unlock()

	artifact, err := c.Compile(task.Request)

	task.mu.Lock()
	defer task.mu.Unlock()

	now := time.Now()
	task.DoneAt = &now

	if err != nil {
		task.Status = CompileStatusFailed
		task.Error = err.Error()
	} else {
		task.Status = CompileStatusComplete
		task.Result = artifact
	}
}

// GetTaskStatus returns the current state of an async compilation task.
func (c *Compiler) GetTaskStatus(taskID string) (*compileTask, error) {
	c.taskManager.mu.RLock()
	defer c.taskManager.mu.RUnlock()

	task, ok := c.taskManager.tasks[taskID]
	if !ok {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	return task.snapshot(), nil
}

// snapshot returns a safe copy of the task's public fields under read lock.
func (t *compileTask) snapshot() *compileTask {
	t.mu.RLock()
	defer t.mu.RUnlock()
	snap := &compileTask{
		ID:        t.ID,
		Request:   t.Request,
		Status:    t.Status,
		Error:     t.Error,
		StartedAt: t.StartedAt,
	}
	if t.DoneAt != nil {
		doneAt := *t.DoneAt
		snap.DoneAt = &doneAt
	}
	if t.Result != nil {
		snap.Result = t.Result
	}
	return snap
}

// NeedsAsync returns true if the compilation requires async execution
// because at least one field requires LLM inference and LLM is available.
func (c *Compiler) NeedsAsync(req CompileRequest) bool {
	if c.llm == nil {
		return false
	}
	if req.CompileMode == CompileModeDeterministic {
		return false
	}

	template := c.resolveTemplate(req)
	mode := c.resolveMode(req, template)
	if mode == CompileModeDeterministic {
		return false
	}

	schema := c.resolveSchema(req, template)
	for _, fd := range schema.Properties {
		if fd.LLM {
			return true
		}
	}
	return false
}

// needsLLMForField returns true if a specific field requires LLM compilation.
func (c *Compiler) needsLLMForField(fieldDef FieldDef, mode CompileMode) bool {
	if mode == CompileModeDeterministic {
		return false
	}
	if !fieldDef.LLM {
		return false
	}
	if c.llm == nil {
		return false
	}
	return true
}

var taskIDCounter uint64

func generateTaskID() string {
	taskIDCounter++
	return fmt.Sprintf("compile_%d_%d", time.Now().UnixNano(), taskIDCounter)
}
