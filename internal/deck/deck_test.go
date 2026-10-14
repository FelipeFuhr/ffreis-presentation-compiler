package deck_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
)

// writeDeck lays out a minimal deck directory and returns its root.
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

const manifest = `
title: Deck Title
subtitle: A subtitle
client: Some Client
theme: flemming
aspect: "16:9"
footer: Flemming
`

func TestLoadReadsManifestAndSlidesInFilenameOrder(t *testing.T) {
	root := writeDeck(t, manifest, map[string]string{
		"02-second.md": "---\nlayout: bullets\ntitle: Second\n---\n",
		"01-first.md":  "---\nlayout: cover\ntitle: First\nkicker: Proposta\n---\nSome **prose**.\n",
	})

	d, err := deck.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if d.Title != "Deck Title" || d.Client != "Some Client" || d.Theme != "flemming" {
		t.Errorf("manifest not parsed: %+v", d)
	}
	if len(d.Slides) != 2 {
		t.Fatalf("want 2 slides, got %d", len(d.Slides))
	}
	if d.Slides[0].Title != "First" || d.Slides[1].Title != "Second" {
		t.Errorf("slides out of filename order: %q then %q", d.Slides[0].Title, d.Slides[1].Title)
	}
	if d.Slides[0].Number != 1 || d.Slides[1].Number != 2 {
		t.Errorf("slides numbered %d,%d; want 1,2", d.Slides[0].Number, d.Slides[1].Number)
	}
	if !strings.Contains(string(d.Slides[0].Body), "<strong>prose</strong>") {
		t.Errorf("markdown body not rendered: %q", d.Slides[0].Body)
	}
	if d.Slides[1].Body != "" {
		t.Errorf("empty body should render empty, got %q", d.Slides[1].Body)
	}
}

func TestLoadKeepsNonChromeKeysAsLayoutFields(t *testing.T) {
	root := writeDeck(t, manifest, map[string]string{
		"01.md": "---\nlayout: table\ntitle: T\ncolumns: [A, B]\nhighlight_row: 2\nsource: IBGE\n---\n",
	})

	d, err := deck.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	fields := d.Slides[0].Fields

	if _, leaked := fields["layout"]; leaked {
		t.Error("chrome key \"layout\" leaked into layout fields")
	}
	if _, leaked := fields["title"]; leaked {
		t.Error("chrome key \"title\" leaked into layout fields")
	}
	if got := d.Slides[0].Field("source"); got != "IBGE" {
		t.Errorf("Field(source) = %q, want IBGE", got)
	}
	if got := d.Slides[0].Field("highlight_row"); got != "" {
		t.Errorf("Field on a non-string should be empty, got %q", got)
	}
	if got := d.Slides[0].Field("absent"); got != "" {
		t.Errorf("Field on a missing key should be empty, got %q", got)
	}
	if _, ok := fields["columns"]; !ok {
		t.Error("layout field \"columns\" was dropped")
	}
}

func TestLoadSkipsSlidesMarkedSkipAndRenumbers(t *testing.T) {
	root := writeDeck(t, manifest, map[string]string{
		"01.md": "---\nlayout: cover\ntitle: Keep\n---\n",
		"02.md": "---\nlayout: cover\ntitle: Drop\nskip: true\n---\n",
		"03.md": "---\nlayout: cover\ntitle: Also keep\n---\n",
	})

	d, err := deck.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(d.Slides) != 2 {
		t.Fatalf("want 2 slides after skip, got %d", len(d.Slides))
	}
	if d.Slides[1].Title != "Also keep" || d.Slides[1].Number != 2 {
		t.Errorf("skipped slide left a gap: %q is number %d", d.Slides[1].Title, d.Slides[1].Number)
	}
}

func TestLoadRejectsBadInput(t *testing.T) {
	cases := []struct {
		name    string
		slide   string
		wantErr string
	}{
		{"missing layout", "---\ntitle: No layout\n---\n", `missing required key "layout"`},
		{"no front-matter", "just a body\n", "front-matter fence"},
		{"unclosed front-matter", "---\nlayout: cover\n", "never closed"},
		{"malformed yaml", "---\nlayout: [unclosed\n---\n", "parse front-matter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := writeDeck(t, manifest, map[string]string{"01.md": tc.slide})
			_, err := deck.Load(root)
			if err == nil {
				t.Fatalf("want an error mentioning %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadRejectsMissingOrEmptyDeck(t *testing.T) {
	t.Run("no manifest", func(t *testing.T) {
		if _, err := deck.Load(t.TempDir()); err == nil {
			t.Fatal("want an error for a deck with no deck.yaml")
		}
	})

	t.Run("malformed manifest", func(t *testing.T) {
		root := writeDeck(t, "title: [unclosed\n", map[string]string{"01.md": "---\nlayout: cover\n---\n"})
		if _, err := deck.Load(root); err == nil {
			t.Fatal("want an error for a malformed deck.yaml")
		}
	})

	t.Run("no slides", func(t *testing.T) {
		root := writeDeck(t, manifest, map[string]string{})
		_, err := deck.Load(root)
		if err == nil || !strings.Contains(err.Error(), "no slides found") {
			t.Fatalf("want a \"no slides found\" error, got %v", err)
		}
	})

	t.Run("every slide skipped", func(t *testing.T) {
		root := writeDeck(t, manifest, map[string]string{"01.md": "---\nlayout: cover\nskip: true\n---\n"})
		_, err := deck.Load(root)
		if err == nil || !strings.Contains(err.Error(), "is skipped") {
			t.Fatalf("want an \"all skipped\" error, got %v", err)
		}
	})
}

func TestSizeResolvesAspect(t *testing.T) {
	cases := []struct {
		aspect  string
		wantW   float64
		wantErr bool
	}{
		{"", 13.333, false},
		{"16:9", 13.333, false},
		{"4:3", 10, false},
		{"21:9", 0, true},
	}
	for _, tc := range cases {
		t.Run("aspect "+tc.aspect, func(t *testing.T) {
			d := &deck.Deck{Aspect: tc.aspect}
			size, err := d.Size()
			if tc.wantErr {
				if err == nil {
					t.Fatal("want an error for an unknown aspect")
				}
				return
			}
			if err != nil {
				t.Fatalf("Size: %v", err)
			}
			if size.WidthIn != tc.wantW || size.HeightIn != 7.5 {
				t.Errorf("got %vx%v, want %vx7.5", size.WidthIn, size.HeightIn, tc.wantW)
			}
		})
	}
}
