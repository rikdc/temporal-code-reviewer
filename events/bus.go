package events

import (
	"sync"

	"github.com/rikdc/temporal-code-reviewer/types"
)

// Publisher abstracts event publishing for testability.
type Publisher interface {
	Publish(event types.WorkflowEvent)
}

// Subscriber abstracts event subscription for testability.
type Subscriber interface {
	Subscribe(workflowID string) chan types.WorkflowEvent
	Unsubscribe(workflowID string, eventChan chan types.WorkflowEvent)
}

// EventBus provides in-memory pub/sub for workflow progress events
type EventBus struct {
	mu          sync.RWMutex
	subscribers map[string][]chan types.WorkflowEvent
}

// NewEventBus creates a new event bus
func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[string][]chan types.WorkflowEvent),
	}
}

// Subscribe registers a new subscriber for a workflow's events
func (eb *EventBus) Subscribe(workflowID string) chan types.WorkflowEvent {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	eventChan := make(chan types.WorkflowEvent, 100) // Buffered to prevent blocking
	eb.subscribers[workflowID] = append(eb.subscribers[workflowID], eventChan)
	return eventChan
}

// Unsubscribe removes a subscriber
func (eb *EventBus) Unsubscribe(workflowID string, eventChan chan types.WorkflowEvent) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	subscribers := eb.subscribers[workflowID]
	for i, ch := range subscribers {
		if ch == eventChan {
			// Remove this channel from the slice
			eb.subscribers[workflowID] = append(subscribers[:i], subscribers[i+1:]...)
			close(eventChan)
			break
		}
	}

	// Clean up empty subscriber lists
	if len(eb.subscribers[workflowID]) == 0 {
		delete(eb.subscribers, workflowID)
	}
}

// Publish sends an event to all subscribers for a workflow
func (eb *EventBus) Publish(event types.WorkflowEvent) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	subscribers := eb.subscribers[event.WorkflowID]
	for _, ch := range subscribers {
		select {
		case ch <- event:
			// Event sent successfully
		default:
			// Channel is full, skip this subscriber to avoid blocking
		}
	}
}
