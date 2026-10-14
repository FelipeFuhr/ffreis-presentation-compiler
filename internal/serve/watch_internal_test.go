package serve

// scan and watchTargets are unexported, so this file lives in package serve
// rather than serve_test.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScanChangesWhenAWatchedFileSizeChanges(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "deck.yaml")
	writeFile(t, file, "title: a")

	before := scan([]string{dir})
	writeFile(t, file, "title: a much longer value than before")

	after := scan([]string{dir})
	if before == after {
		t.Fatal("fingerprint did not change after a file's size changed")
	}
}

func TestScanChangesWhenAWatchedFileMtimeChanges(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "deck.yaml")
	writeFile(t, file, "title: a")
	before := scan([]string{dir})

	// Same content, same size — only the modification time moves. A touch
	// with no content change (common with some editors) must still be
	// detected.
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(file, future, future); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	after := scan([]string{dir})
	if before == after {
		t.Fatal("fingerprint did not change after a file's mtime changed")
	}
}

func TestScanIsStableWhenNothingChanges(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "deck.yaml"), "title: a")
	writeFile(t, filepath.Join(dir, "slides", "01-cover.md"), "---\nlayout: cover\n---\nbody")

	first := scan([]string{dir})
	second := scan([]string{dir})
	if first != second {
		t.Fatalf("fingerprint changed with no modification: %q != %q", first, second)
	}
}

func TestScanChangesWhenAFileIsAddedOrRemoved(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "deck.yaml"), "title: a")
	before := scan([]string{dir})

	added := filepath.Join(dir, "slides", "02-new.md")
	writeFile(t, added, "---\nlayout: cover\n---\n")
	afterAdd := scan([]string{dir})
	if before == afterAdd {
		t.Fatal("fingerprint did not change after a file was added")
	}

	if err := os.Remove(added); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	afterRemove := scan([]string{dir})
	if afterAdd == afterRemove {
		t.Fatal("fingerprint did not change after a file was removed")
	}
}

func TestScanSkipsAMissingRootInsteadOfFailing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	// Must not panic and must be deterministic: a theme root is missing
	// until the first successful render learns the theme name.
	a := scan([]string{missing})
	b := scan([]string{missing})
	if a != b {
		t.Fatalf("fingerprint of a missing root is not stable: %q != %q", a, b)
	}
}

func TestWatchTargetsOmitsTheThemeDirUntilItIsKnown(t *testing.T) {
	targets := watchTargets("/decks/q3", "/themes", "")
	for _, target := range targets {
		if filepath.Dir(target) == "/themes" {
			t.Fatalf("theme dir was watched before a theme name was resolved: %v", targets)
		}
	}
}

func TestWatchTargetsIncludesTheResolvedThemeDir(t *testing.T) {
	targets := watchTargets("/decks/q3", "/themes", "sample")
	want := filepath.Join("/themes", "sample", "css")
	if !contains(targets, want) {
		t.Fatalf("watch targets do not include %q: %v", want, targets)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
