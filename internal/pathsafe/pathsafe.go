// Package pathsafe is the single place every deck- or theme-author-supplied
// path is resolved against the root it is supposed to live under.
//
// filepath.Join alone does not stop ".." traversal: Join merely concatenates
// and cleans, so Join("/a/deck", "../../etc") happily resolves to "/etc". And
// a naive strings.HasPrefix check on the joined absolute path is its own
// trap — root "/a/deck" has the same string prefix as the unrelated sibling
// "/a/deckx/f". filepath.Rel is what actually answers "does this still live
// under root", which is why Contain is built on it instead.
package pathsafe

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Contain resolves candidate against root and returns the absolute result,
// erroring when the cleaned path would not stay inside root. Every path a
// deck or theme author supplies — a theme name, a chart/diagram spec or data
// file, a slide's media — must pass through here before it is opened.
func Contain(root, candidate string) (string, error) {
	if candidate == "" {
		return "", fmt.Errorf("empty path is not valid under %s", root)
	}
	if filepath.IsAbs(candidate) {
		return "", fmt.Errorf("%q is an absolute path, not relative to %s", candidate, root)
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", root, err)
	}
	joined := filepath.Join(absRoot, candidate)

	rel, err := filepath.Rel(absRoot, joined)
	if err != nil {
		return "", fmt.Errorf("resolve %q against %s: %w", candidate, root, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%q escapes %s", candidate, root)
	}
	return joined, nil
}
