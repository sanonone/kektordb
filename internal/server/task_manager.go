package server

import (
	"github.com/google/uuid"
	"sync"
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
	mu              sync.RWMutex
}

// TaskManager tracks all running asynchronous tasks.
type TaskManager struct {
	tasks map[string]*Task
	mu    sync.RWMutex
}

// NewTaskManager creates a new task manager.
func NewTaskManager() *TaskManager {
	return &TaskManager{
		tasks: make(map[string]*Task),
	}
}

// NewTask creates a new task, registers it, and returns it.
func (tm *TaskManager) NewTask() *Task {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	task := &Task{
		ID:     uuid.New().String(), // Generate a unique ID
		Status: TaskStatusStarted,
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
