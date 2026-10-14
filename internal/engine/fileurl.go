package engine

import (
	"fmt"
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
func FileURL(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("file URL for %s: %w", path, err)
	}
	return (&url.URL{Scheme: "file", Path: abs}).String(), nil
}
