package live

import (
	"sync"
	"time"
)

// Event is a lightweight "something changed" notification. ID is the
// event_envelopes.id -- the same cursor space as live.watermark.
type Event struct {
	ID      uint        `json:"id"`
	Type    string      `json:"type"`
	Subject string      `json:"subject"`
	At      time.Time   `json:"at"`
	Payload interface{} `json:"payload,omitempty"`
}

// RFC-010: heartbeat-style noise must not be streamed.
func IsNoise(eventType string) bool {
	return eventType == "task.progress"
}

type Hub struct {
	mu          sync.Mutex
	subscribers map[chan Event]bool
}

var GlobalHub = &Hub{subscribers: make(map[chan Event]bool)}

func (h *Hub) Subscribe() chan Event {
	h.mu.Lock()
	defer h.mu.Unlock()

	// 256, not 16: while a reconnecting client is being replayed, live
	// events buffer here. A full buffer silently drops events for that client.
	ch := make(chan Event, 256)
	h.subscribers[ch] = true
	return ch
}

func (h *Hub) Unsubscribe(ch chan Event) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Publish may already have removed and closed it.
	if h.subscribers[ch] {
		delete(h.subscribers, ch)
		close(ch)
	}
}

func (h *Hub) Publish(event Event) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for ch := range h.subscribers {
		select {
		case ch <- event:
		default:
			delete(h.subscribers, ch)
			close(ch)
		}
	}
}
