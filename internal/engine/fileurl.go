package engine

import (
	"net/url"
	"path/filepath"
)

// FileURL builds a file:// URL for path, suitable for handing to a browser.
//
// The naive "file://" + path concatenation used to live at every call site
// here. It breaks the moment path contains a character a URL treats
// specially: '#' truncates everything after it as a fragment, so Chrome
// loads the wrong document (or nothing); '?' starts a query string; '%'
// begins a percent-escape; and a literal space is unreliable across tools.
// Deck and theme roots routinely contain exactly these — non-ASCII brand
// names, client slugs with a stray '#' or space — so this is not a
// theoretical concern.
//
// path is absolutized first: a relative file:// URL resolves against
// nothing, which would trade this bug for a more confusing one.
//
// This deliberately returns no error. filepath.Abs fails only when
// os.Getwd() does — i.e. the process's working directory has been removed
// from under it — and every caller has already stat'd this same path, so
// that condition surfaces there first and with a better message. An error
// return here bought four call sites an `if err != nil` branch that no
// caller could reach.
func FileURL(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		// The working directory is gone, so a relative path cannot be
		// resolved. It must still be made root-relative: url.URL renders a
		// Path with no leading separator as "file://relative/index.html",
		// which parses back with Host="relative" — a silently wrong URL
		// rather than an obviously wrong one.
		abs = "/" + filepath.ToSlash(path)
	}
	return (&url.URL{Scheme: "file", Path: abs}).String()
}
