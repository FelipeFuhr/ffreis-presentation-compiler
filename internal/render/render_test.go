package render_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/render"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/theme"
)

const (
	sampleDeck   = "../../examples/sample-deck"
	sampleThemes = "../../examples/themes"
)

// renderSample compiles the shipped example deck into a temp dir and returns
// the output dir, the rendered HTML and the deck it came from.
func renderSample(t *testing.T) (string, string, *deck.Deck) {
	t.Helper()
	d, err := deck.Load(sampleDeck)
	if err != nil {
		t.Fatalf("load deck: %v", err)
	}
	th, err := theme.Load(sampleThemes, d.Theme)
	if err != nil {
		t.Fatalf("load theme: %v", err)
	}
	out := t.TempDir()
	if err := render.Render(d, th, render.Options{DeckRoot: sampleDeck, OutDir: out}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	html, err := os.ReadFile(filepath.Join(out, render.IndexFile))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	return out, string(html), d
}

func TestRenderWritesOneSectionPerSlide(t *testing.T) {
	_, html, d := renderSample(t)

	// Derived from the deck, not hardcoded: the invariant is one section per
	// slide, and adding an example slide must not break the test.
	if got, want := strings.Count(html, "<section"), len(d.Slides); got != want {
		t.Errorf("rendered %d sections, want %d (one per slide)", got, want)
	}
	// Pagination is the contract the PDF path depends on.
	if !strings.Contains(html, "@page { size: 13.333in 7.5in; margin: 0; }") {
		t.Error("document shell is missing the @page rule that sizes every PDF page")
	}
	if !strings.Contains(html, `data-slide="1"`) ||
		!strings.Contains(html, fmt.Sprintf(`data-slide="%d"`, len(d.Slides))) {
		t.Error("slides are not numbered in the markup")
	}
	if !strings.Contains(html, "--accent") {
		t.Error("theme CSS was not inlined")
	}
}

func TestRenderPassesSlideContentToLayouts(t *testing.T) {
	_, html, _ := renderSample(t)

	if !strings.Contains(html, "Unit economics against comparable sites") {
		t.Error("slide titles are missing")
	}
	if !strings.Contains(html, `class="is-highlighted"`) {
		t.Error("the table layout did not apply highlight_row")
	}
	if !strings.Contains(html, "<strong>not</strong>") {
		t.Error("Markdown bodies are not rendered into layouts")
	}
	if !strings.Contains(html, "Sample Industries · Operations") {
		t.Error("deck footer is missing from chrome")
	}
}

func TestRenderCopiesDeckAssets(t *testing.T) {
	out, html, _ := renderSample(t)

	asset := filepath.Join(out, "assets", "throughput.svg")
	if _, err := os.Stat(asset); err != nil {
		t.Errorf("deck asset was not copied next to index.html: %v", err)
	}
	if !strings.Contains(html, `src="assets/throughput.svg"`) {
		t.Error("asset is referenced by a path that will not resolve from index.html")
	}
}

func TestRenderCopiesThemeAssets(t *testing.T) {
	out, _, _ := renderSample(t)

	if _, err := os.Stat(filepath.Join(out, "assets", "logo.svg")); err != nil {
		t.Errorf("theme asset was not copied: %v", err)
	}
}

// sampleFixtures loads the example deck and theme for tests that then mutate
// the deck in memory.
func sampleFixtures(t *testing.T) (*deck.Deck, *theme.Theme) {
	t.Helper()
	d, err := deck.Load(sampleDeck)
	if err != nil {
		t.Fatalf("load deck: %v", err)
	}
	th, err := theme.Load(sampleThemes, d.Theme)
	if err != nil {
		t.Fatalf("load theme: %v", err)
	}
	return d, th
}

func TestRenderCopiesNestedAssetDirectories(t *testing.T) {
	d, th := sampleFixtures(t)
	deckRoot := t.TempDir()
	nested := filepath.Join(deckRoot, "assets", "logos", "clients")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "acme.svg"), []byte("<svg/>"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	out := t.TempDir()
	if err := render.Render(d, th, render.Options{DeckRoot: deckRoot, OutDir: out}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "assets", "logos", "clients", "acme.svg")); err != nil {
		t.Errorf("nested asset was not copied: %v", err)
	}
}

func TestRenderFailsWhenAnAssetCannotBeWritten(t *testing.T) {
	d, th := sampleFixtures(t)
	deckRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(deckRoot, "assets"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(deckRoot, "assets", "clash.svg"), []byte("<svg/>"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	// A directory already occupying the destination filename.
	out := t.TempDir()
	if err := os.MkdirAll(filepath.Join(out, "assets", "clash.svg"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	err := render.Render(d, th, render.Options{DeckRoot: deckRoot, OutDir: out})
	if err == nil || !strings.Contains(err.Error(), "clash.svg") {
		t.Fatalf("want an error naming the asset that could not be written, got %v", err)
	}
}

func TestRenderFailsWhenTheOutputDirCannotBeCreated(t *testing.T) {
	d, err := deck.Load(sampleDeck)
	if err != nil {
		t.Fatalf("load deck: %v", err)
	}
	th, err := theme.Load(sampleThemes, d.Theme)
	if err != nil {
		t.Fatalf("load theme: %v", err)
	}
	// A regular file where the output directory should go.
	blocker := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := render.Render(d, th, render.Options{DeckRoot: sampleDeck, OutDir: blocker}); err == nil {
		t.Fatal("want an error when the output directory cannot be created")
	}
}

func TestRenderFailsLoudlyOnAnUnknownLayout(t *testing.T) {
	d, err := deck.Load(sampleDeck)
	if err != nil {
		t.Fatalf("load deck: %v", err)
	}
	th, err := theme.Load(sampleThemes, d.Theme)
	if err != nil {
		t.Fatalf("load theme: %v", err)
	}
	d.Slides[1].Layout = "does-not-exist"

	err = render.Render(d, th, render.Options{DeckRoot: sampleDeck, OutDir: t.TempDir()})
	if err == nil {
		t.Fatal("want an error for a layout the theme does not provide")
	}
	// The message has to locate the problem: slide number and source file.
	if !strings.Contains(err.Error(), "02-headline.md") {
		t.Errorf("error %q does not name the offending slide file", err)
	}
}

func TestRenderRejectsAnUnknownAspect(t *testing.T) {
	d, err := deck.Load(sampleDeck)
	if err != nil {
		t.Fatalf("load deck: %v", err)
	}
	th, err := theme.Load(sampleThemes, d.Theme)
	if err != nil {
		t.Fatalf("load theme: %v", err)
	}
	d.Aspect = "21:9"

	if err := render.Render(d, th, render.Options{DeckRoot: sampleDeck, OutDir: t.TempDir()}); err == nil {
		t.Fatal("want an error for an unsupported aspect")
	}
}

func TestRenderTreatsAMissingAssetDirAsFine(t *testing.T) {
	// A deck with no assets/ is legitimate and must not fail the build.
	d, err := deck.Load(sampleDeck)
	if err != nil {
		t.Fatalf("load deck: %v", err)
	}
	th, err := theme.Load(sampleThemes, d.Theme)
	if err != nil {
		t.Fatalf("load theme: %v", err)
	}
	opts := render.Options{DeckRoot: t.TempDir(), OutDir: t.TempDir()}
	if err := render.Render(d, th, opts); err != nil {
		t.Errorf("Render with no deck assets: %v", err)
	}
}

func TestRenderRejectsAnAssetPathThatIsAFile(t *testing.T) {
	d, err := deck.Load(sampleDeck)
	if err != nil {
		t.Fatalf("load deck: %v", err)
	}
	th, err := theme.Load(sampleThemes, d.Theme)
	if err != nil {
		t.Fatalf("load theme: %v", err)
	}
	deckRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(deckRoot, "assets"), []byte("not a dir"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	err = render.Render(d, th, render.Options{DeckRoot: deckRoot, OutDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("want a \"not a directory\" error, got %v", err)
	}
}
