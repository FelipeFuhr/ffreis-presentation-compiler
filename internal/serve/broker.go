package serve

import (
	"fmt"
	"net/http"
	"sync"
)

// broker fans out a reload signal to every connected /__reload client over
// Server-Sent Events. SSE needs no client-side library: the script injected
// into the served page is a bare EventSource plus location.reload().
type broker struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

func newBroker() *broker {
	return &broker{subs: make(map[chan struct{}]struct{})}
}

func (b *broker) subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *broker) unsubscribe(ch chan struct{}) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
}

// publish nudges every connected client. A client that already has a signal
// pending is left alone — one reload after a burst of changes is enough.
func (b *broker) publish() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// serveSSE streams reload events to one client until it disconnects.
func (b *broker) serveSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch := b.subscribe()
	defer b.unsubscribe(ch)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			if _, err := fmt.Fprint(w, "data: reload\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
