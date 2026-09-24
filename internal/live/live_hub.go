package live

import "sync"

// A minimal in-process pub/sub. One backend instance, one process -- no
// Redis needed for this. If this project ever runs multiple backend
// instances behind a load balancer, THIS is the file that gets replaced with
// Redis Pub/Sub (which RFC-010 explicitly names as one valid transport) --
// nothing else in the live package needs to change, since callers only ever
// see Publish/Subscribe/Unsubscribe.

type Event struct {
	Type    string      `json:"type"` // "alert.opened", "alert.resolved", etc
	Payload interface{} `json:"payload"`
}

type Hub struct {
	mu          sync.Mutex
	subscribers map[chan Event]bool
}

var GlobalHub = &Hub{subscribers: make(map[chan Event]bool)}

// Subscribe returns a channel that receives every future Publish call.
// Buffered so a slow client doesn't block the publisher -- if a client falls
// behind, events are dropped for THAT client rather than stalling everyone.
func (h *Hub) Subscribe() chan Event {
	h.mu.Lock()
	defer h.mu.Unlock()

	ch := make(chan Event, 16)
	h.subscribers[ch] = true
	return ch
}

func (h *Hub) Unsubscribe(ch chan Event) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.subscribers, ch)
	close(ch)
}

func (h *Hub) Publish(event Event) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for ch := range h.subscribers {
		select {
		case ch <- event:
		default:
			// buffer full -- this one subscriber is behind, drop the event
			// for them rather than blocking every other subscriber
		}
	}
}
