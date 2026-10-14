package serve

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"
)

// pollInterval is how often the watch loop rescans the deck and theme trees
// for changes.
//
// Polling a cheap fingerprint, rather than fsnotify, is deliberate: it adds
// no new dependency, it is immune to the editor-writes-via-rename pattern
// that makes inotify miss saves from some editors and tools, and a deck is a
// few dozen small files — a scan this cheap costs nothing at this interval.
//
// A var, not a const: tests shorten it to drive the watch loop in
// milliseconds instead of real authoring-sized intervals, restoring it on
// cleanup. Production code never changes it.
var pollInterval = 300 * time.Millisecond

// debounceWindow coalesces the handful of mtime changes one save usually
// produces (a temp file, a rename, a directory mtime bump) into a single
// rebuild. A var for the same test-speed reason as pollInterval.
var debounceWindow = 250 * time.Millisecond

// fingerprint is a cheap snapshot of a set of directory trees: it changes
// when a watched file's size or modification time changes, or when a file is
// added or removed, and is stable otherwise. It never reads file content, so
// computing it costs proportional to the number of entries, not their size.
type fingerprint string

// scan folds every watched root's path, size and mtime into a fingerprint. A
// root that does not exist — an optional directory like assets/, or a theme
// not resolved yet — is skipped rather than failing the whole scan.
func scan(roots []string) fingerprint {
	h := sha256.New()
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				// A missing or unreadable root is not a watch failure; it
				// simply contributes nothing to the fingerprint.
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			// hash.Hash.Write never returns an error; the fmt.Fprintf plumbing
			// just goes through io.Writer.
			_, _ = fmt.Fprintf(h, "%s|%d|%d\n", path, info.Size(), info.ModTime().UnixNano())
			return nil
		})
	}
	return fingerprint(hex.EncodeToString(h.Sum(nil)))
}

// watchedDeckEntries are the deck-root subpaths that make up a deck's
// authoring surface.
var watchedDeckEntries = []string{"deck.yaml", "slides", "assets", "charts", "data"}

// watchedThemeEntries are the subpaths of a resolved theme directory that
// affect its rendered output.
var watchedThemeEntries = []string{"theme.yaml", "css", "layouts", "slide.gohtml"}

// watchTargets returns the deck's authoring surface plus the one theme
// directory the deck currently resolves to, so editing a theme file reloads
// the preview too — that is most of the authoring loop. Until the first
// successful render, themeName is unknown and only the deck root is watched.
func watchTargets(deckRoot, themeRoot, themeName string) []string {
	targets := make([]string, 0, len(watchedDeckEntries)+len(watchedThemeEntries))
	for _, e := range watchedDeckEntries {
		targets = append(targets, filepath.Join(deckRoot, e))
	}
	if themeName == "" {
		return targets
	}
	dir := filepath.Join(themeRoot, themeName)
	for _, e := range watchedThemeEntries {
		targets = append(targets, filepath.Join(dir, e))
	}
	return targets
}
