package httpserver

import "sync"

type hub struct {
	mu    sync.Mutex
	rooms map[string]map[chan struct{}]struct{}
}

func newHub() *hub { return &hub{rooms: make(map[string]map[chan struct{}]struct{})} }

func (h *hub) subscribe(room string) (<-chan struct{}, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan struct{}, 1)
	if h.rooms[room] == nil {
		h.rooms[room] = make(map[chan struct{}]struct{})
	}
	h.rooms[room][ch] = struct{}{}
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.rooms[room], ch)
		if len(h.rooms[room]) == 0 {
			delete(h.rooms, room)
		}
	}
}

func (h *hub) broadcast(room string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.rooms[room] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
