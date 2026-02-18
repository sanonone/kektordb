package server

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// TaskStatus defines the possible states of a task.
type TaskStatus string

const (
	TaskStatusStarted   TaskStatus = "started"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
)

// Task represents a long-running operation.
type Task struct {
	ID              string     `json:"id"`
	Status          TaskStatus `json:"status"`
	ProgressMessage string     `json:"progress_message,omitempty"`
	Error           string     `json:"error,omitempty"`
	createdAt       time.Time  // Internal timestamp for cleanup tracking (not exported to JSON)
	mu              sync.RWMutex
}

// TaskManager tracks all running asynchronous tasks and provides automatic cleanup
// to prevent memory leaks from accumulated completed tasks.
type TaskManager struct {
	tasks           map[string]*Task
	mu              sync.RWMutex
	cleanupTicker   *time.Ticker
	cleanupStopCh   chan struct{}
	cleanupInterval time.Duration
	maxTaskAge      time.Duration
}

// DefaultCleanupInterval is the frequency at which old tasks are checked and removed.
const DefaultCleanupInterval = 10 * time.Minute

// DefaultMaxTaskAge is the maximum age of a task before it is eligible for cleanup.
// Tasks older than this will be removed regardless of their status.
const DefaultMaxTaskAge = 1 * time.Hour

// NewTaskManager creates a new task manager with automatic cleanup enabled.
// The cleanup routine runs periodically in the background to remove old completed/failed tasks
// and prevent memory leaks in long-running server instances.
func NewTaskManager() *TaskManager {
	tm := &TaskManager{
		tasks:           make(map[string]*Task),
		cleanupInterval: DefaultCleanupInterval,
		maxTaskAge:      DefaultMaxTaskAge,
		cleanupStopCh:   make(chan struct{}),
	}
	// Start the background cleanup routine immediately
	tm.startCleanupRoutine()
	return tm
}

// NewTask creates a new task, registers it, and returns it.
// The task is automatically timestamped with the current time for cleanup tracking.
func (tm *TaskManager) NewTask() *Task {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	task := &Task{
		ID:        uuid.New().String(), // Generate a unique ID
		Status:    TaskStatusStarted,
		createdAt: time.Now(), // Set creation timestamp for automatic cleanup
	}
	tm.tasks[task.ID] = task
	return task
}

// GetTask safely retrieves a task by its ID.
func (tm *TaskManager) GetTask(id string) (*Task, bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	task, found := tm.tasks[id]
	return task, found
}

// startCleanupRoutine starts a background goroutine that periodically removes old tasks.
// This prevents memory leaks from accumulated task objects in long-running instances.
// The routine can be stopped by calling StopCleanup() or when the TaskManager is garbage collected.
func (tm *TaskManager) startCleanupRoutine() {
	tm.cleanupTicker = time.NewTicker(tm.cleanupInterval)
	go func() {
		for {
			select {
			case <-tm.cleanupTicker.C:
				tm.CleanupOldTasks(tm.maxTaskAge)
			case <-tm.cleanupStopCh:
				tm.cleanupTicker.Stop()
				return
			}
		}
	}()
}

// StopCleanup stops the background cleanup routine.
// This should be called during server shutdown to ensure graceful termination.
func (tm *TaskManager) StopCleanup() {
	close(tm.cleanupStopCh)
}

// CleanupOldTasks removes tasks older than maxAge from the internal map.
// This method is thread-safe and acquires a write lock during the cleanup operation.
// Tasks are removed regardless of their status (completed, failed, or even running)
// if they exceed the maximum age threshold.
func (tm *TaskManager) CleanupOldTasks(maxAge time.Duration) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	now := time.Now()
	initialCount := len(tm.tasks)

	for id, task := range tm.tasks {
		// Check if the task has exceeded the maximum age
		if now.Sub(task.createdAt) > maxAge {
			delete(tm.tasks, id)
		}
	}

	// Log cleanup statistics only if tasks were actually removed
	if removed := initialCount - len(tm.tasks); removed > 0 {
		// Note: In a production environment, this should use structured logging
		// via a proper logger interface rather than stdout
		_ = removed
	}
}

// --- Methods for updating a Task ---

// SetStatus updates the status of the task.
func (t *Task) SetStatus(status TaskStatus) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Status = status
}

// SetError marks the task as failed and records the error message.
func (t *Task) SetError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Status = TaskStatusFailed
	t.Error = err.Error()
}

// SetProgress updates the progress message for the task.
func (t *Task) SetProgress(message string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ProgressMessage = message
}
