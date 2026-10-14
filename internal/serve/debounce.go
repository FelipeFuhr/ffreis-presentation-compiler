package serve

import (
	"sync"
	"time"
)

// debouncer coalesces rapid-fire calls to trigger into a single call to fn,
// fired once delay has passed with no further trigger call. A single file
// save often produces several mtime changes in quick succession (a temp
// file, a rename, a directory mtime bump); this turns all of them into one
// rebuild instead of one per change.
type debouncer struct {
	mu    sync.Mutex
	timer *time.Timer
	delay time.Duration
	fn    func()
}

func newDebouncer(delay time.Duration, fn func()) *debouncer {
	return &debouncer{delay: delay, fn: fn}
}

// trigger resets the quiet window.
func (d *debouncer) trigger() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = time.AfterFunc(d.delay, d.fn)
}

// stop cancels any pending call. Used on shutdown so a rebuild cannot fire
// after the watch loop has already returned.
func (d *debouncer) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		d.timer.Stop()
	}
}
