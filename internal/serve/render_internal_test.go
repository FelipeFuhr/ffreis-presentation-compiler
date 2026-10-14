package serve

// renderOnce is unexported, so this file lives in package serve rather than
// serve_test.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sampleDeck and sampleThemes are declared in handler_internal_test.go;
// this file is the same package, so it reuses them.

// writeDeck lays out a minimal deck directory (deck.yaml + slides/*.md) and
// returns its root, mirroring internal/deck's own test fixture builder.
func writeDeck(t *testing.T, manifest string, slides map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "deck.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	dir := filepath.Join(root, "slides")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir slides: %v", err)
	}
	for name, body := range slides {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write slide %s: %v", name, err)
		}
	}
	return root
}

func TestRenderOnceFailsWhenTheDeckRootHasNoManifest(t *testing.T) {
	_, err := renderOnce(Options{DeckRoot: t.TempDir(), ThemeRoot: sampleThemes}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "deck.yaml") {
		t.Fatalf("err = %v, want it to name the missing deck.yaml", err)
	}
}

func TestRenderOnceFailsWhenTheNamedThemeDoesNotExist(t *testing.T) {
	root := writeDeck(t, "theme: nope-this-theme-does-not-exist\n", map[string]string{
		"01-cover.md": "---\nlayout: cover\ntitle: Hi\n---\n",
	})

	_, err := renderOnce(Options{DeckRoot: root, ThemeRoot: sampleThemes}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "nope-this-theme-does-not-exist") {
		t.Fatalf("err = %v, want it to name the unresolved theme", err)
	}
}

func TestRenderOnceFailsOnAnUnknownLayout(t *testing.T) {
	root := writeDeck(t, "theme: sample\n", map[string]string{
		"01-cover.md": "---\nlayout: no-such-layout\ntitle: Hi\n---\n",
	})

	_, err := renderOnce(Options{DeckRoot: root, ThemeRoot: sampleThemes}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "no-such-layout") {
		t.Fatalf("err = %v, want it to name the unknown layout", err)
	}
}

func TestRenderOnceFailsWhenAChartNamesAnUnknownGenerator(t *testing.T) {
	root := writeDeck(t, "theme: sample\n", map[string]string{
		"01-cover.md": "---\nlayout: cover\ntitle: Hi\nchart:\n  generator: not-a-real-generator\n  spec: x.py\n---\n",
	})

	_, err := renderOnce(Options{DeckRoot: root, ThemeRoot: sampleThemes}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "not-a-real-generator") {
		t.Fatalf("err = %v, want it to name the unknown generator", err)
	}
}

// TestRenderOnceSucceedsAndReportsThemeNameAndSlideCount locks in the two
// fields the rebuild loop depends on: themeName (to decide what to watch)
// and slideCount (logged on every rebuild).
func TestRenderOnceSucceedsAndReportsThemeNameAndSlideCount(t *testing.T) {
	res, err := renderOnce(Options{DeckRoot: sampleDeck, ThemeRoot: sampleThemes, Lang: "en"}, t.TempDir())
	if err != nil {
		t.Fatalf("renderOnce: %v", err)
	}
	if res.themeName != "sample" {
		t.Errorf("themeName = %q, want sample", res.themeName)
	}
	if res.slideCount != 7 {
		t.Errorf("slideCount = %d, want 7", res.slideCount)
	}
	if len(res.html) == 0 {
		t.Error("html is empty")
	}
}
