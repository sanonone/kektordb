package compiler

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// compileTask represents an asynchronous compilation operation.
type compileTask struct {
	ID      string         `json:"id"`
	Request CompileRequest `json:"request"`
	Status  CompileStatus  `json:"status"`
	Result  *Artifact      `json:"result,omitempty"`
	Error   string         `json:"error,omitempty"`

	StartedAt time.Time  `json:"started_at"`
	DoneAt    *time.Time `json:"done_at,omitempty"`

	mu sync.RWMutex
}

// compileTaskManager tracks in-flight and completed async compilations.
// Completed tasks are automatically evicted after defaultTaskTTL to prevent
// unbounded memory growth (E6 fix).
type compileTaskManager struct {
	tasks  map[string]*compileTask
	mu     sync.RWMutex
	ttl    time.Duration
	stopCh chan struct{}
}

const defaultTaskTTL = 24 * time.Hour

func newCompileTaskManager() *compileTaskManager {
	return newCompileTaskManagerWithTTL(defaultTaskTTL)
}

// newCompileTaskManagerWithTTL is the testable constructor.
func newCompileTaskManagerWithTTL(ttl time.Duration) *compileTaskManager {
	tm := &compileTaskManager{
		tasks:  make(map[string]*compileTask),
		ttl:    ttl,
		stopCh: make(chan struct{}),
	}
	go tm.sweepLoop()
	return tm
}

// sweepLoop periodically removes completed tasks older than the TTL.
func (tm *compileTaskManager) sweepLoop() {
	// Sweep every hour, or more frequently if TTL is shorter (for tests).
	interval := 1 * time.Hour
	if tm.ttl < interval {
		interval = tm.ttl / 2
		if interval < time.Second {
			interval = time.Second
		}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			tm.sweep()
		case <-tm.stopCh:
			return
		}
	}
}

// sweep removes all tasks whose DoneAt is older than the TTL.
func (tm *compileTaskManager) sweep() {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	now := time.Now()
	for id, task := range tm.tasks {
		if task.DoneAt != nil && now.Sub(*task.DoneAt) > tm.ttl {
			delete(tm.tasks, id)
		}
	}
}

// Close stops the background sweep goroutine.
func (tm *compileTaskManager) Close() {
	select {
	case <-tm.stopCh:
	default:
		close(tm.stopCh)
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

var taskIDCounter atomic.Uint64

func generateTaskID() string {
	c := taskIDCounter.Add(1)
	return fmt.Sprintf("compile_%d_%d", time.Now().UnixNano(), c)
}
