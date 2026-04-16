package engine

import "sync"

// EventType categorizes engine events for pub/sub consumers.
type EventType string

const (
	EventVectorAdd    EventType = "vector.add"
	EventVectorDelete EventType = "vector.delete"
	EventEdgeCreate   EventType = "edge.create"
	EventEdgeDelete   EventType = "edge.delete"
	EventVectorAccess EventType = "vector.access"
	EventVectorUpdate EventType = "vector.update"
	EventEvolution    EventType = "memory.evolution"
)

// Event represents a single write operation in the engine.
type Event struct {
	Type      EventType `json:"type"`
	IndexName string    `json:"index_name"`
	ID        string    `json:"id"`
	TargetID  string    `json:"target_id,omitempty"`
	RelType   string    `json:"rel_type,omitempty"`
	Timestamp int64     `json:"timestamp"`
}

// EventBus implements a fan-out pub/sub pattern for engine events.
// Each subscriber gets its own channel. Events are broadcast to all active subscribers.
// If a subscriber's buffer is full, the event is silently dropped for that subscriber
// to prevent slowing down the database write-path.
type EventBus struct {
	subscribers map[chan Event]struct{}
	mu          sync.RWMutex
}

// NewEventBus creates a new event bus.
func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[chan Event]struct{}),
	}
}

// Subscribe returns a new channel that will receive all future events.
// The caller is responsible for reading from this channel to avoid blocking,
// and MUST call Unsubscribe when done.
func (eb *EventBus) Subscribe(bufferSize int) chan Event {
	ch := make(chan Event, bufferSize)
	eb.mu.Lock()
	eb.subscribers[ch] = struct{}{}
	eb.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber and closes its channel.
func (eb *EventBus) Unsubscribe(ch chan Event) {
	eb.mu.Lock()
	if _, ok := eb.subscribers[ch]; ok {
		delete(eb.subscribers, ch)
		close(ch)
	}
	eb.mu.Unlock()
}

// Emit broadcasts the event to all active subscribers.
// If a subscriber's buffer is full, the event is silently dropped for that subscriber
// to prevent slowing down the database write-path.
func (eb *EventBus) Emit(e Event) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	for ch := range eb.subscribers {
		select {
		case ch <- e:
		default:
			// Buffer full, drop the event for this slow consumer.
		}
	}
}

// Close shuts down the bus and closes all subscriber channels.
func (eb *EventBus) Close() {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	for ch := range eb.subscribers {
		close(ch)
	}
	eb.subscribers = make(map[chan Event]struct{})
}
