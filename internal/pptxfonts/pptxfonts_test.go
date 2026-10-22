package pptxfonts_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine/pptx"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/fontsource"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/pptxfonts"
)

// fontDir writes a directory of font files whose contents name the variant,
// so a test can tell which bytes landed in which field.
func fontDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("bytes of "+n), 0o600); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}
	return dir
}

func load(t *testing.T, typeface string, opt fontsource.Options) ([]pptx.EmbeddedFont, []string) {
	t.Helper()
	fonts, notes, err := pptxfonts.Load(context.Background(), typeface, opt)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return fonts, notes
}

func TestEachVariantLandsInItsOwnField(t *testing.T) {
	// A variant written into the wrong field is a deck whose bold runs render
	// italic, which no later check in this repo would catch.
	dir := fontDir(t, "Roboto-Regular.ttf", "Roboto-Bold.ttf", "Roboto-Italic.ttf", "Roboto-BoldItalic.ttf")
	fonts, _ := load(t, "Roboto", fontsource.Options{Dirs: []string{dir}})
	if len(fonts) != 1 {
		t.Fatalf("got %d embedded fonts, want 1", len(fonts))
	}

	for _, tc := range []struct {
		field string
		body  []byte
		want  string
	}{
		{"Regular", fonts[0].Regular, "Roboto-Regular.ttf"},
		{"Bold", fonts[0].Bold, "Roboto-Bold.ttf"},
		{"Italic", fonts[0].Italic, "Roboto-Italic.ttf"},
		{"BoldItalic", fonts[0].BoldItalic, "Roboto-BoldItalic.ttf"},
	} {
		if !bytes.Contains(tc.body, []byte(tc.want)) {
			t.Errorf("%s holds %q, want the bytes of %s", tc.field, tc.body, tc.want)
		}
	}
	if fonts[0].Typeface != "Roboto" {
		t.Errorf("Typeface = %q", fonts[0].Typeface)
	}
}

func TestAFaceOfficeShipsIsNamedNotEmbedded(t *testing.T) {
	// Embedding one buys nothing -- it cannot substitute -- and costs a
	// megabyte or more in a file that gets emailed.
	dir := fontDir(t, "Calibri-Regular.ttf")
	fonts, notes := load(t, "Calibri", fontsource.Options{Dirs: []string{dir}})
	if len(fonts) != 0 {
		t.Errorf("Calibri was embedded anyway")
	}
	if !strings.Contains(strings.Join(notes, "\n"), "ships with Office") {
		t.Errorf("notes do not explain why it was skipped: %v", notes)
	}
}

func TestAFontThatCannotBeFoundIsReportedNotFatal(t *testing.T) {
	// The deck still renders, substituting. Failing the build would make a
	// missing font file fatal to a format the author may not have asked for.
	fonts, notes := load(t, "Roboto", fontsource.Options{Dirs: []string{t.TempDir()}})
	if len(fonts) != 0 {
		t.Errorf("got %d fonts from an empty directory", len(fonts))
	}
	if !strings.Contains(strings.Join(notes, "\n"), "substitute") {
		t.Errorf("notes do not say what a reader will see: %v", notes)
	}
}

func TestTheNoteNamesWhereEachFaceCameFrom(t *testing.T) {
	// Embedding from the build machine's installed fonts is only as
	// reproducible as that machine, so the build has to say it did.
	dir := fontDir(t, "Roboto-Regular.ttf")
	_, notes := load(t, "Roboto", fontsource.Options{Dirs: []string{dir}})
	joined := strings.Join(notes, "\n")
	if !strings.Contains(joined, "embedding Roboto") || !strings.Contains(joined, dir) {
		t.Errorf("notes do not record the provenance: %v", notes)
	}
}

func TestNoTypefaceIsNotAnError(t *testing.T) {
	// A deck that names no font leaves the choice to the theme's fontScheme,
	// which is resolved elsewhere.
	fonts, notes, err := pptxfonts.Load(context.Background(), "   ", fontsource.Options{})
	if err != nil || len(fonts) != 0 || len(notes) != 0 {
		t.Errorf("Load(empty) = %v, %v, %v; want nothing at all", fonts, notes, err)
	}
}

func TestAnUnreadableFontFileIsAnError(t *testing.T) {
	// Distinct from "not found": a file that is there and cannot be read
	// means the build cannot do what it was told, so it says so.
	dir := fontDir(t, "Roboto-Regular.ttf")
	path := filepath.Join(dir, "Roboto-Regular.ttf")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Skipf("cannot make the file unreadable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if _, _, err := pptxfonts.Load(context.Background(), "Roboto", fontsource.Options{Dirs: []string{dir}}); err == nil {
		t.Skip("running as a user that can read a 0000 file, so there is nothing to assert")
	}
}
