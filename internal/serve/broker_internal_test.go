package serve

// broker is unexported, so this file lives in package serve rather than
// serve_test.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBrokerPublishDeliversToASubscriber(t *testing.T) {
	b := newBroker()
	ch := b.subscribe()
	defer b.unsubscribe(ch)

	b.publish()

	select {
	case <-ch:
	default:
		t.Fatal("subscriber did not receive the published signal")
	}
}

func TestBrokerPublishDoesNotBlockOnAFullSubscriber(t *testing.T) {
	b := newBroker()
	ch := b.subscribe()
	defer b.unsubscribe(ch)

	// publish twice before the subscriber reads: a reload signal already
	// pending is enough, so this must not deadlock on the buffered channel.
	b.publish()
	b.publish()

	<-ch
}

func TestBrokerPublishWithNoSubscribersIsANoop(t *testing.T) {
	b := newBroker()
	b.publish() // must not panic with an empty subscriber set
}

func TestUnsubscribeStopsFurtherDelivery(t *testing.T) {
	b := newBroker()
	ch := b.subscribe()
	b.unsubscribe(ch)

	b.publish()
	// ch is no longer tracked by the broker; nothing should arrive on it.
	select {
	case <-ch:
		t.Fatal("unsubscribed channel still received a signal")
	default:
	}
}

// headerOnlyWriter implements http.ResponseWriter but deliberately not
// http.Flusher — httptest.NewRecorder() does implement Flush(), so it cannot
// exercise this branch.
type headerOnlyWriter struct {
	header http.Header
	body   strings.Builder
	code   int
}

func newHeaderOnlyWriter() *headerOnlyWriter {
	return &headerOnlyWriter{header: make(http.Header)}
}

func (w *headerOnlyWriter) Header() http.Header         { return w.header }
func (w *headerOnlyWriter) Write(b []byte) (int, error) { return w.body.Write(b) }
func (w *headerOnlyWriter) WriteHeader(code int)        { w.code = code }

func TestReloadEndpointRejectsAResponseWriterThatCannotFlush(t *testing.T) {
	b := newBroker()
	w := newHeaderOnlyWriter()
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/__reload", nil)

	b.serveSSE(w, req)

	if w.code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.code, http.StatusInternalServerError)
	}
	if !strings.Contains(w.body.String(), "streaming unsupported") {
		t.Errorf("body = %q, want it to explain why", w.body.String())
	}
}

// syncSSEWriter is a minimal, concurrency-safe http.ResponseWriter +
// http.Flusher. httptest.ResponseRecorder is not safe for a handler goroutine
// to write to while a test goroutine reads it, which is exactly what
// exercising serveSSE's event-delivery branch needs — the handler is still
// running (blocked on its next select) when the test inspects what it wrote.
type syncSSEWriter struct {
	mu      sync.Mutex
	header  http.Header
	body    strings.Builder
	code    int
	flushes int
}

func newSyncSSEWriter() *syncSSEWriter {
	return &syncSSEWriter{header: make(http.Header)}
}

func (w *syncSSEWriter) Header() http.Header { return w.header }

func (w *syncSSEWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.Write(b)
}

func (w *syncSSEWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.code = code
}

func (w *syncSSEWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushes++
}

func (w *syncSSEWriter) flushCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.flushes
}

func (w *syncSSEWriter) bodyString() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.String()
}

// TestReloadEndpointDeliversAPublishedEventThenStopsOnDisconnect exercises
// serveSSE's main loop: the connect flush, one delivered event (the
// `<-ch` branch), and the return once the client's context is canceled.
func TestReloadEndpointDeliversAPublishedEventThenStopsOnDisconnect(t *testing.T) {
	b := newBroker()
	w := newSyncSSEWriter()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequestWithContext(ctx, "GET", "/__reload", nil)

	done := make(chan struct{})
	go func() {
		b.serveSSE(w, req)
		close(done)
	}()

	// Wait for the handler to connect (its first Flush, right after headers)
	// before publishing, so the event is guaranteed to land on an active
	// subscriber rather than racing subscribe().
	waitUntil(t, time.Second, func() bool { return w.flushCount() >= 1 })
	b.publish()
	waitUntil(t, time.Second, func() bool { return w.flushCount() >= 2 })

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("serveSSE did not return after the client disconnected")
	}

	if !strings.Contains(w.bodyString(), "data: reload") {
		t.Errorf("body = %q, want a reload event", w.bodyString())
	}
}
