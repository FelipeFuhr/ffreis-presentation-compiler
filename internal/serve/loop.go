package serve

import (
	"context"
	"sync"
	"time"
)

// rebuild re-renders the deck and publishes the result: a successful render
// replaces the served page and clears any banner; a failed one keeps serving
// the last good page with the error overlaid. Either way connected browsers
// are nudged to reload, so the author sees the new state (good or bad)
// without touching the browser.
func (s *Server) rebuild() {
	start := time.Now()
	res, err := renderOnce(s.opts, s.outDir)
	if err != nil {
		s.site.setErr(err.Error())
		s.logger.Error("rebuild failed", "err", err)
		s.broker.publish()
		return
	}
	s.setThemeName(res.themeName)
	s.site.setGood(injectReload(res.html))
	s.logger.Info("rebuilt", "duration", time.Since(start), "slides", res.slideCount)
	s.broker.publish()
}

// watchLoop polls for changes and triggers a debounced rebuild until ctx is
// done.
func (s *Server) watchLoop(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	db := newDebouncer(debounceWindow, s.rebuild)
	defer db.stop()

	last := scan(s.targets())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cur := scan(s.targets())
			if cur != last {
				last = cur
				db.trigger()
			}
		}
	}
}

// targets returns the current watch set, which depends on the theme name
// learned from the last successful render.
func (s *Server) targets() []string {
	return watchTargets(s.opts.DeckRoot, s.opts.ThemeRoot, s.currentThemeName())
}

// themeState protects the theme name watchLoop reads: rebuild can run from a
// debounce timer's own goroutine, concurrently with the poll loop.
type themeState struct {
	mu   sync.Mutex
	name string
}

func (s *Server) setThemeName(name string) {
	s.theme.mu.Lock()
	s.theme.name = name
	s.theme.mu.Unlock()
}

func (s *Server) currentThemeName() string {
	s.theme.mu.Lock()
	defer s.theme.mu.Unlock()
	return s.theme.name
}
