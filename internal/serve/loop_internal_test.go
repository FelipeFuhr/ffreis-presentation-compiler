package serve

// rebuild and watchLoop are unexported, so this file lives in package serve
// rather than serve_test.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withFastTimings shortens the poll interval and debounce window for the
// duration of one test, so the watch loop can be driven in milliseconds
// instead of real authoring-sized intervals. Not parallel-safe — none of
// these tests call t.Parallel().
func withFastTimings(t *testing.T) {
	t.Helper()
	origPoll, origDebounce := pollInterval, debounceWindow
	pollInterval = 5 * time.Millisecond
	debounceWindow = 5 * time.Millisecond
	t.Cleanup(func() {
		pollInterval = origPoll
		debounceWindow = origDebounce
	})
}

// waitUntil polls cond until it is true or the deadline passes, failing the
// test in the latter case. Used instead of a single fixed sleep so these
// tests run fast on an idle machine and still pass on a loaded one.
func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

// drainOne reads one value off ch within a second, failing the test if none
// arrives — used to confirm rebuild published a reload signal.
func drainOne(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("expected a published reload signal, got none")
	}
}

// TestRebuildKeepsLastGoodPageAndPublishesWhenARenderFails is the
// error-recovery requirement observed by hand during manual verification (a
// YAML typo must not take the server down): a failing rebuild must not
// discard the previous good render, and must still publish a reload signal
// so a connected browser sees the error immediately.
func TestRebuildKeepsLastGoodPageAndPublishesWhenARenderFails(t *testing.T) {
	s := newTestServer(t, Options{DeckRoot: sampleDeck, ThemeRoot: sampleThemes, Lang: "en"})
	ch := s.broker.subscribe()
	defer s.broker.unsubscribe(ch)

	s.rebuild()
	drainOne(t, ch)
	if !strings.Contains(string(s.site.page()), "Quarterly Operations Review") {
		t.Fatalf("first rebuild did not produce the expected good page:\n%s", s.site.page())
	}

	s.opts.DeckRoot = t.TempDir() // no deck.yaml here: this render must fail
	s.rebuild()
	drainOne(t, ch) // a failing rebuild still nudges connected browsers to reload

	after := string(s.site.page())
	if !strings.Contains(after, "Quarterly Operations Review") {
		t.Fatalf("last good content was lost after a failed rebuild:\n%s", after)
	}
	if !strings.Contains(after, "rebuild failed") {
		t.Fatalf("the rebuild error is not visible on the page:\n%s", after)
	}

	// Recovery: fixing the input must clear the banner on the next rebuild.
	s.opts.DeckRoot = sampleDeck
	s.rebuild()
	drainOne(t, ch)
	recovered := string(s.site.page())
	if strings.Contains(recovered, "rebuild failed") {
		t.Fatalf("error banner survived a successful rebuild:\n%s", recovered)
	}
}

// TestWatchLoopRebuildsOnChangeThenStopsOnCancel drives the real watch loop
// (not a direct rebuild call) end to end: a file change under the deck root
// must be detected and debounced into exactly one rebuild, and canceling the
// context must stop the loop.
func TestWatchLoopRebuildsOnChangeThenStopsOnCancel(t *testing.T) {
	withFastTimings(t)

	deckRoot := writeDeck(t, "theme: sample\n", map[string]string{
		"01-cover.md": "---\nlayout: cover\ntitle: Hi\n---\n",
	})
	s := newTestServer(t, Options{DeckRoot: deckRoot, ThemeRoot: sampleThemes, Lang: "en"})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.watchLoop(ctx)
		close(done)
	}()

	if got := s.site.page(); len(got) != 0 {
		t.Fatalf("watchLoop rebuilt before any change was made: %q", got)
	}
	// Let the loop take its first baseline scan before touching anything —
	// otherwise the touch could land before the baseline and be invisible to
	// the very first comparison.
	time.Sleep(20 * pollInterval)

	slide := filepath.Join(deckRoot, "slides", "01-cover.md")
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(slide, future, future); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	waitUntil(t, 2*time.Second, func() bool { return len(s.site.page()) > 0 })

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchLoop did not return after its context was canceled")
	}
}
