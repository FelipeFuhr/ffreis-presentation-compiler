package pptx_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine/pptx"
)

// probeSfnt is the fixture every embedding test hands in.
//
// It has to be a real font: the embedding path reads the OS/2, head and name
// tables to build the EOT header a reader matches against. The fifteen bytes
// of "probe font data" this replaced were embedded happily by the old writer,
// because nothing read them. See buildSfnt in eot_test.go for why the font is
// synthesised rather than shipped.
func probeSfnt(t *testing.T) []byte {
	return buildSfnt(t, fontSpec{Family: "ZZProbeFace", Style: "Regular", Weight: 400, OS2Version: 1})
}

func fontDeck(fonts ...pptx.EmbeddedFont) pptx.Deck {
	return pptx.Deck{
		WidthIn: 13.333, HeightIn: 7.5, Lang: "en", Font: "ZZProbeFace",
		EmbeddedFonts: fonts,
		Slides: []pptx.Slide{{Shapes: []pptx.Shape{{
			Kind: pptx.ShapeText, At: pptx.Rect{XIn: 1, YIn: 1, WIn: 5, HIn: 1},
			Text: "probe", Style: pptx.TextStyle{SizePt: 18, Font: "ZZProbeFace"},
		}}}},
	}
}

func writeDeckTo(t *testing.T, d pptx.Deck) (string, map[string]string) {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "deck.pptx")
	if err := pptx.Write(dst, d); err != nil {
		t.Fatalf("Write: %v", err)
	}
	parts, _ := readAll(t, dst)
	return dst, parts
}

// ── the typeface a deck names ───────────────────────────────────────────────

func TestTheThemeFontSchemeCarriesTheDecksTypeface(t *testing.T) {
	// The shipped theme asset used to hardcode one consumer's font, which is
	// the same leak as this package's old pt-BR notes default.
	_, parts := writeDeckTo(t, fontDeck())
	for _, part := range []string{"ppt/theme/theme1.xml", "ppt/theme/theme2.xml"} {
		body := parts[part]
		if strings.Contains(body, "__LATIN_TYPEFACE__") {
			t.Errorf("%s still carries the placeholder", part)
		}
		if n := strings.Count(body, `typeface="ZZProbeFace"`); n != 2 {
			t.Errorf("%s names the deck's typeface %d times, want 2 (major and minor)", part, n)
		}
	}
}

func TestADeckThatNamesNoFontGetsOneOfficeShips(t *testing.T) {
	// Empty must not reach the fontScheme: a theme declaring no typeface would
	// otherwise ship an empty one, which PowerPoint resolves to its own
	// default rather than anything the deck chose.
	d := fontDeck()
	d.Font = ""
	d.Slides[0].Shapes[0].Style.Font = ""
	_, parts := writeDeckTo(t, d)

	want := `typeface="` + pptx.DefaultFont + `"`
	if !strings.Contains(parts["ppt/theme/theme1.xml"], want) {
		t.Errorf("fontScheme does not fall back to %s", pptx.DefaultFont)
	}
	if !pptx.AssumedPresent(pptx.DefaultFont) {
		t.Errorf("DefaultFont %q must be one that cannot substitute", pptx.DefaultFont)
	}
}

func TestAssumedPresentIsCaseAndSpaceInsensitive(t *testing.T) {
	for _, in := range []string{"Calibri", "calibri", "  CALIBRI  ", "Times New Roman"} {
		if !pptx.AssumedPresent(in) {
			t.Errorf("AssumedPresent(%q) = false", in)
		}
	}
	for _, in := range []string{"Roboto", "Inter", "", "Comic Papyrus"} {
		if pptx.AssumedPresent(in) {
			t.Errorf("AssumedPresent(%q) = true; the list is a promise about other people's machines", in)
		}
	}
}

// ── embedding ───────────────────────────────────────────────────────────────

func TestEmbeddingWiresThePartTheRelationshipAndTheDeclaration(t *testing.T) {
	font := probeSfnt(t)
	_, parts := writeDeckTo(t, fontDeck(pptx.EmbeddedFont{
		Typeface: "ZZProbeFace", Regular: font, Bold: font,
	}))

	// The data parts exist and carry the font inside an EOT container.
	//
	// This used to assert the part was byte-identical to the sfnt handed in,
	// which passed for as long as the writer shipped the .ttf with no header
	// -- the defect. What matters is that the font is in there AND that a
	// reader has a header to recognise it by.
	for _, name := range []string{"ppt/fonts/font1-regular.fntdata", "ppt/fonts/font1-bold.fntdata"} {
		got := parts[name]
		if !strings.Contains(got, string(font)) {
			t.Errorf("%s does not carry the font data", name)
		}
		if len(got) <= len(font) {
			t.Errorf("%s is %d bytes for a %d-byte font, so it has no header", name, len(got), len(font))
		}
	}
	// Typed, or PowerPoint cannot interpret them.
	if !strings.Contains(parts["[Content_Types].xml"], `Extension="fntdata"`) {
		t.Error("fntdata has no content type")
	}
	// Declared, and the declaration resolves.
	pres := parts["ppt/presentation.xml"]
	if !strings.Contains(pres, `<p:font typeface="ZZProbeFace"/>`) {
		t.Error("presentation.xml does not declare the embedded typeface")
	}
	ids := regexp.MustCompile(`<p:(?:regular|bold) r:id="(rId\d+)"`).FindAllStringSubmatch(pres, -1)
	if len(ids) != 2 {
		t.Fatalf("want regular and bold declared, got %d", len(ids))
	}
	rels := parts["ppt/_rels/presentation.xml.rels"]
	for _, m := range ids {
		if !strings.Contains(rels, `Id="`+m[1]+`"`) {
			t.Errorf("%s has no relationship", m[1])
		}
	}
}

func TestEmbeddingIsAbsentWhenNoFontIsGiven(t *testing.T) {
	_, parts := writeDeckTo(t, fontDeck())
	if strings.Contains(parts["ppt/presentation.xml"], "embeddedFontLst") {
		t.Error("a deck with no embedded fonts should declare none")
	}
	// And the content type should not claim an extension nothing uses.
	if strings.Contains(parts["[Content_Types].xml"], "fntdata") {
		t.Error("fntdata is declared but no font part exists")
	}
}

func TestAnEmbeddedFontWithNoDataIsNotDeclared(t *testing.T) {
	// Declaring a typeface with no variants would reach PowerPoint as a font
	// it was promised and cannot find.
	_, parts := writeDeckTo(t, fontDeck(pptx.EmbeddedFont{Typeface: "ZZProbeFace"}))
	if strings.Contains(parts["ppt/presentation.xml"], "embeddedFontLst") {
		t.Error("an embedded font with no variants should be dropped, not declared empty")
	}
}

func TestEmbeddedFontsSurviveVerification(t *testing.T) {
	dst, _ := writeDeckTo(t, fontDeck(pptx.EmbeddedFont{
		Typeface: "ZZProbeFace", Regular: probeSfnt(t), Italic: probeSfnt(t),
	}))
	findings, err := pptx.Verify(dst)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	for _, f := range findings {
		t.Errorf("embedding produced a finding: %s", f)
	}
}

func TestVerifyCatchesFontDataNothingDeclares(t *testing.T) {
	// Orphan font data usually means a declaration was dropped rather than
	// the bytes, and it travels in every copy of the file.
	parts := explode(t, mustBytes(t, fontDeck()))
	// Content irrelevant: the rule is about a part nothing declares, which is
	// decided by the manifest and not by whether the bytes are a real font.
	parts = append(parts, namedPart{"ppt/fonts/font9-regular.fntdata", []byte("orphaned")})
	findings := verifyBytes(t, rezip(t, parts))
	if rules(findings)[pptx.RuleFontEmbedding] == 0 {
		t.Errorf("want a font-embedding finding, got %v", rules(findings))
	}
}

func TestVerifyCatchesADeclarationWithNoData(t *testing.T) {
	raw := mustBytes(t, fontDeck(pptx.EmbeddedFont{Typeface: "ZZProbeFace", Regular: probeSfnt(t)}))
	parts := explode(t, raw)
	kept := parts[:0]
	for _, p := range parts {
		if p.name != "ppt/fonts/font1-regular.fntdata" {
			kept = append(kept, p)
		}
	}
	findings := verifyBytes(t, rezip(t, kept))
	if rules(findings)[pptx.RuleFontEmbedding] == 0 {
		t.Errorf("want a font-embedding finding for a declaration whose data is gone, got %v", rules(findings))
	}
}

// ── the report ──────────────────────────────────────────────────────────────

func TestFontsReportsWhatAReaderWillSee(t *testing.T) {
	// Embedded: safe.
	dst, _ := writeDeckTo(t, fontDeck(pptx.EmbeddedFont{Typeface: "ZZProbeFace", Regular: probeSfnt(t)}))
	reports := mustFonts(t, dst)
	got := find(reports, "ZZProbeFace")
	if got == nil || !got.Embedded || !got.Safe() {
		t.Errorf("embedded typeface reported as %+v", got)
	}

	// Named but not embedded and not a system font: will substitute.
	dst2, _ := writeDeckTo(t, fontDeck())
	got2 := find(mustFonts(t, dst2), "ZZProbeFace")
	if got2 == nil || got2.Embedded || got2.Safe() {
		t.Errorf("an unembedded unknown typeface should be unsafe, got %+v", got2)
	}
	if !strings.Contains(got2.String(), "substitute") {
		t.Errorf("the message should say what happens: %q", got2.String())
	}
}

func TestFontsIgnoresThemeReferences(t *testing.T) {
	// "+mj-lt" and "+mn-lt" are references into the fontScheme, not typefaces.
	// Reporting them was a false positive on every build, which is how a
	// warning stops being read.
	dst, _ := writeDeckTo(t, fontDeck())
	for _, r := range mustFonts(t, dst) {
		if strings.HasPrefix(r.Typeface, "+") {
			t.Errorf("theme reference %q reported as a font", r.Typeface)
		}
	}
}

func TestFontsReportsASystemFontAsSafe(t *testing.T) {
	d := fontDeck()
	d.Font = "Georgia"
	d.Slides[0].Shapes[0].Style.Font = "Georgia"
	dst, _ := writeDeckTo(t, d)
	got := find(mustFonts(t, dst), "Georgia")
	if got == nil || got.Embedded || !got.AssumedPresent || !got.Safe() {
		t.Errorf("Georgia should be safe without embedding, got %+v", got)
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

func mustBytes(t *testing.T, d pptx.Deck) []byte {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "deck.pptx")
	if err := pptx.Write(dst, d); err != nil {
		t.Fatalf("Write: %v", err)
	}
	raw, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return raw
}

func mustFonts(t *testing.T, path string) []pptx.FontReport {
	t.Helper()
	r, err := pptx.Fonts(path)
	if err != nil {
		t.Fatalf("Fonts: %v", err)
	}
	return r
}

func find(reports []pptx.FontReport, typeface string) *pptx.FontReport {
	for i := range reports {
		if reports[i].Typeface == typeface {
			return &reports[i]
		}
	}
	return nil
}
