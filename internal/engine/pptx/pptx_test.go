package pptx_test

import (
	"archive/zip"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine/pptx"
)

// onePixelPNG is a valid 1x1 PNG, enough to stand in for a slide render.
var onePixelPNG = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0a, 'I', 'D', 'A', 'T',
	0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05,
	0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00,
	0x00, 0x00, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
}

func writeDeck(t *testing.T, slides ...pptx.Slide) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "deck.pptx")
	d := pptx.Deck{WidthIn: 13.333, HeightIn: 7.5, Slides: slides}
	if err := pptx.Write(dst, d); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return dst
}

func readAll(t *testing.T, path string) (map[string]string, []string) {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer func() { _ = r.Close() }()

	parts := map[string]string{}
	var order []string
	for _, f := range r.File {
		order = append(order, f.Name)
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		parts[f.Name] = string(body)
	}
	return parts, order
}

func TestWriteProducesAReadablePackage(t *testing.T) {
	dst := writeDeck(t,
		pptx.Slide{PNG: onePixelPNG, Notes: "First slide\nwith two lines"},
		pptx.Slide{PNG: onePixelPNG},
	)
	parts, order := readAll(t, dst)

	// OPC readers expect the content-type map first.
	if order[0] != "[Content_Types].xml" {
		t.Errorf("first entry is %q, want [Content_Types].xml", order[0])
	}

	for _, want := range []string{
		"_rels/.rels",
		"ppt/presentation.xml",
		"ppt/_rels/presentation.xml.rels",
		"ppt/slideMasters/slideMaster1.xml",
		"ppt/slideLayouts/slideLayout1.xml",
		"ppt/notesMasters/notesMaster1.xml",
		"ppt/theme/theme1.xml",
		"ppt/slides/slide1.xml",
		"ppt/slides/_rels/slide1.xml.rels",
		"ppt/media/slide1.png",
		"ppt/notesSlides/notesSlide1.xml",
		"ppt/slides/slide2.xml",
		"ppt/media/slide2.png",
	} {
		if _, ok := parts[want]; !ok {
			t.Errorf("package is missing %s", want)
		}
	}
}

// TestLocalHeadersCarrySizes guards the bug that made the first working build
// unreadable: archive/zip's streaming path leaves CRC and sizes out of the
// local header and sets general-purpose bit 3, which LibreOffice rejects
// outright while other readers accept it.
func TestLocalHeadersCarrySizes(t *testing.T) {
	dst := writeDeck(t, pptx.Slide{PNG: onePixelPNG, Notes: "n"})
	raw, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	checked := 0
	for i := 0; i+30 < len(raw); i++ {
		if string(raw[i:i+4]) != "PK\x03\x04" {
			continue
		}
		flag := binary.LittleEndian.Uint16(raw[i+6 : i+8])
		crc := binary.LittleEndian.Uint32(raw[i+14 : i+18])
		csize := binary.LittleEndian.Uint32(raw[i+18 : i+22])
		usize := binary.LittleEndian.Uint32(raw[i+22 : i+26])

		if flag&0x8 != 0 {
			t.Fatalf("entry at offset %d sets the data-descriptor bit", i)
		}
		if crc == 0 || csize == 0 || usize == 0 {
			t.Fatalf("entry at offset %d has an empty local header (crc=%d csize=%d usize=%d)", i, crc, csize, usize)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("found no local file headers to check")
	}
}

func TestWriteIsReproducible(t *testing.T) {
	slides := []pptx.Slide{{PNG: onePixelPNG, Notes: "same"}}
	first, err := os.ReadFile(writeDeck(t, slides...))
	if err != nil {
		t.Fatalf("read first: %v", err)
	}
	second, err := os.ReadFile(writeDeck(t, slides...))
	if err != nil {
		t.Fatalf("read second: %v", err)
	}
	if string(first) != string(second) {
		t.Error("two writes of the same deck produced different bytes")
	}
}

func TestSlideGeometryAndNotes(t *testing.T) {
	dst := writeDeck(t, pptx.Slide{PNG: onePixelPNG, Notes: "Kicker\nThe title"})
	parts, _ := readAll(t, dst)

	// 13.333in and 7.5in in EMU.
	if !strings.Contains(parts["ppt/presentation.xml"], `cx="12191695"`) {
		t.Errorf("slide width is not the deck width:\n%s", parts["ppt/presentation.xml"])
	}
	if !strings.Contains(parts["ppt/slides/slide1.xml"], `cy="6858000"`) {
		t.Error("the picture does not fill the slide height")
	}
	notes := parts["ppt/notesSlides/notesSlide1.xml"]
	for _, want := range []string{"Kicker", "The title"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes are missing %q", want)
		}
	}
}

// TestNotesCarryTheDecksLanguage locks the removal of a hardcoded pt-BR from
// the notes writer — one consumer's language baked into a public,
// consumer-agnostic compiler, which is what the -deck-root/-theme-root
// discipline exists to avoid. It sets only PowerPoint's spell-check language
// for the notes pane, which is why it survived unnoticed; the fallback must
// still be a real tag, because the zero value would write lang="".
func TestNotesCarryTheDecksLanguage(t *testing.T) {
	for _, tc := range []struct {
		name string
		lang string
		want string
	}{
		{"the deck's own tag", "fr-CA", `lang="fr-CA"`},
		{"an unset tag falls back", "", `lang="` + pptx.DefaultNotesLang + `"`},
		{"a blank tag falls back", "   ", `lang="` + pptx.DefaultNotesLang + `"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dst := filepath.Join(t.TempDir(), "deck.pptx")
			d := pptx.Deck{WidthIn: 13.333, HeightIn: 7.5, Lang: tc.lang,
				Slides: []pptx.Slide{{PNG: onePixelPNG, Notes: "a note"}}}
			if err := pptx.Write(dst, d); err != nil {
				t.Fatalf("Write: %v", err)
			}

			parts, _ := readAll(t, dst)
			notes := parts["ppt/notesSlides/notesSlide1.xml"]
			if !strings.Contains(notes, tc.want) {
				t.Errorf("notes do not carry %s:\n%s", tc.want, notes)
			}
			if strings.Contains(notes, "pt-BR") {
				t.Errorf("a consumer's language is still hardcoded in the notes writer:\n%s", notes)
			}
		})
	}
}

func TestNotesAreXMLEscaped(t *testing.T) {
	dst := writeDeck(t, pptx.Slide{PNG: onePixelPNG, Notes: `R&D <ops> "quoted"`})
	parts, _ := readAll(t, dst)
	notes := parts["ppt/notesSlides/notesSlide1.xml"]

	if strings.Contains(notes, "<ops>") {
		t.Error("raw markup leaked into the notes XML")
	}
	if !strings.Contains(notes, "&amp;") || !strings.Contains(notes, "&lt;ops&gt;") {
		t.Errorf("notes were not escaped:\n%s", notes)
	}
}

func TestWriteRejectsAnEmptyOrUnsizedDeck(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "x.pptx")

	if err := pptx.Write(dst, pptx.Deck{WidthIn: 10, HeightIn: 7.5}); err == nil {
		t.Error("want an error for a deck with no slides")
	}
	err := pptx.Write(dst, pptx.Deck{Slides: []pptx.Slide{{PNG: onePixelPNG}}})
	if err == nil || !strings.Contains(err.Error(), "no size") {
		t.Errorf("want a \"no size\" error, got %v", err)
	}
}

func TestWriteReportsAnUnwritableDestination(t *testing.T) {
	err := pptx.Write(filepath.Join(t.TempDir(), "no-such-dir", "x.pptx"),
		pptx.Deck{WidthIn: 10, HeightIn: 7.5, Slides: []pptx.Slide{{PNG: onePixelPNG}}})
	if err == nil {
		t.Fatal("want an error when the destination cannot be created")
	}
}

// TestWriteLeavesNoTempFileOnSuccess guards the mechanism of the atomic-write
// fix itself: the archive is built in a temp file beside dst and renamed into
// place, so a successful Write must leave exactly the destination file behind
// — no leftover ".tmp-*" sibling.
func TestWriteLeavesNoTempFileOnSuccess(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "deck.pptx")
	deck := pptx.Deck{WidthIn: 10, HeightIn: 7.5, Slides: []pptx.Slide{{PNG: onePixelPNG}}}

	if err := pptx.Write(dst, deck); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("destination was not written: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "deck.pptx" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("output dir contains %v, want only deck.pptx", names)
	}
}

// TestWriteLeavesNoFileOnFailureAfterTheArchiveIsBuilt forces a failure at the
// final commit step — the os.Rename into place — by making the destination
// an existing directory rather than a file: POSIX rename(2) refuses to
// replace a directory with a non-directory (EISDIR). writeParts itself can't
// be made to fail from outside the package without an injectable writer, and
// adding one purely for this test would be a seam the production code has no
// other use for. Forcing the failure at the rename step instead exercises the
// exact guarantee the fix adds: even a late failure, after the whole archive
// has been built successfully in the temp file, must not leave a truncated
// (or any) artifact at the destination, and must not leave the temp file
// behind either.
func TestWriteLeavesNoFileOnFailureAfterTheArchiveIsBuilt(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "deck.pptx")
	if err := os.Mkdir(dst, 0o750); err != nil {
		t.Fatalf("pre-create destination as a directory: %v", err)
	}

	deck := pptx.Deck{WidthIn: 10, HeightIn: 7.5, Slides: []pptx.Slide{{PNG: onePixelPNG}}}
	err := pptx.Write(dst, deck)
	if err == nil {
		t.Fatal("want an error when the destination cannot be replaced")
	}

	info, statErr := os.Stat(dst)
	if statErr != nil {
		t.Fatalf("destination vanished entirely: %v", statErr)
	}
	if !info.IsDir() {
		t.Error("destination was replaced by a (presumably truncated) file instead of being left untouched")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("output dir contains %v, want only the untouched deck.pptx directory (no leftover temp file)", names)
	}
}
