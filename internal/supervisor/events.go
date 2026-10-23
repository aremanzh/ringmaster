package supervisor

import (
	"sync"
	"time"
)

// Event is a task status transition, broadcast to API subscribers.
type Event struct {
	Key    string    `json:"key"`
	Status Status    `json:"status"`
	PID    int       `json:"pid,omitempty"`
	Port   int       `json:"port,omitempty"`
	At     time.Time `json:"at"`
}

// eventHub fans status events out to subscribers. Slow subscribers drop
// events rather than blocking supervisor transitions.
type eventHub struct {
	mu   sync.Mutex
	subs map[int]chan Event
	next int
}

func newEventHub() *eventHub {
	return &eventHub{subs: map[int]chan Event{}}
}

func (h *eventHub) publish(e Event) {
	h.mu.Lock()
	for _, ch := range h.subs {
		select {
		case ch <- e:
		default:
		}
	}
	h.mu.Unlock()
}

// Subscribe returns a status-event channel and its cancel func. Cancel is
// safe to call more than once.
func (s *Supervisor) Subscribe() (<-chan Event, func()) {
	h := s.events
	h.mu.Lock()
	id := h.next
	h.next++
	ch := make(chan Event, 256)
	h.subs[id] = ch
	h.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, id)
			h.mu.Unlock()
		})
	}
}

// emit publishes a status transition. Safe to call with or without s.mu
// held — it only reads its arguments.
func (s *Supervisor) emit(key string, status Status, pid, port int) {
	s.events.publish(Event{Key: key, Status: status, PID: pid, Port: port, At: time.Now()})
}
