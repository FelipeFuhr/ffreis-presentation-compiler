package serve

// debouncer is unexported, so this file lives in package serve rather than
// serve_test.

import (
	"sync/atomic"
	"testing"
	"time"
)

// Generous relative to delay so the assertions are not sensitive to
// scheduler jitter on a loaded CI runner.
const testDebounceDelay = 20 * time.Millisecond

func TestDebouncerCoalescesRapidTriggersIntoOneCall(t *testing.T) {
	var calls atomic.Int32
	d := newDebouncer(testDebounceDelay, func() { calls.Add(1) })

	for i := 0; i < 5; i++ {
		d.trigger()
		time.Sleep(testDebounceDelay / 4)
	}

	time.Sleep(testDebounceDelay * 5)
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls = %d, want 1 for a burst of triggers inside the debounce window", got)
	}
}

func TestDebouncerFiresAgainForASeparateBurst(t *testing.T) {
	var calls atomic.Int32
	d := newDebouncer(testDebounceDelay, func() { calls.Add(1) })

	d.trigger()
	time.Sleep(testDebounceDelay * 5)
	d.trigger()
	time.Sleep(testDebounceDelay * 5)

	if got := calls.Load(); got != 2 {
		t.Fatalf("calls = %d, want 2 for two separate, well-spaced bursts", got)
	}
}

func TestDebouncerStopCancelsAPendingCall(t *testing.T) {
	var calls atomic.Int32
	d := newDebouncer(testDebounceDelay, func() { calls.Add(1) })

	d.trigger()
	d.stop()
	time.Sleep(testDebounceDelay * 5)

	if got := calls.Load(); got != 0 {
		t.Fatalf("calls = %d, want 0 after stop canceled the pending call", got)
	}
}
