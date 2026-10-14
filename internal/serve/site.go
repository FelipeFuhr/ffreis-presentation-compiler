package serve

import "sync"

// site holds what gets served at "/" and "/index.html": the last
// successfully rendered (and reload-injected) HTML, plus the error from the
// most recent failed rebuild, if any.
//
// A failed rebuild never discards the last good render — losing the whole
// preview over a YAML typo would make the tool useless mid-edit.
type site struct {
	mu   sync.RWMutex
	good []byte
	err  string
}

func newSite() *site {
	return &site{}
}

// setGood records a successful render and clears any previous error.
func (s *site) setGood(html []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.good = html
	s.err = ""
}

// setErr records a failed rebuild without discarding the last good render.
func (s *site) setErr(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = msg
}

// page returns what "/" should serve right now.
func (s *site) page() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch {
	case s.err == "":
		return s.good
	case len(s.good) == 0:
		return standaloneErrorPage(s.err)
	default:
		return injectErrorBanner(s.good, s.err)
	}
}
