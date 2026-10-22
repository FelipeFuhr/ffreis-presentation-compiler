package pptxcheck_test

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/pptxcheck"
)

// bbox builds pdftotext -bbox output from word tuples, so a test states the
// geometry it means rather than a blob of XML.
func bbox(pages ...[]pptxcheck.Word) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><html><body><doc>`)
	for _, words := range pages {
		b.WriteString(`<page width="960.00" height="540.00">`)
		for _, w := range words {
			fmt.Fprintf(&b, `<word xMin="%f" yMin="%f" xMax="%f" yMax="%f">%s</word>`,
				w.X0, w.Y0, w.X1, w.Y1, w.Text)
		}
		b.WriteString(`</page>`)
	}
	b.WriteString(`</doc></body></html>`)
	return []byte(b.String())
}

func word(x0, y0, x1, y1 float64, text string) pptxcheck.Word {
	return pptxcheck.Word{X0: x0, Y0: y0, X1: x1, Y1: y1, Text: text}
}

func parse(t *testing.T, raw []byte) []pptxcheck.Page {
	t.Helper()
	pages, err := pptxcheck.ParseBBox(raw)
	if err != nil {
		t.Fatalf("ParseBBox: %v", err)
	}
	return pages
}

func rules(findings []pptxcheck.Finding) map[string]int {
	out := map[string]int{}
	for _, f := range findings {
		out[f.Rule]++
	}
	return out
}

func allNative(n int) map[int]bool {
	out := map[int]bool{}
	for i := 1; i <= n; i++ {
		out[i] = true
	}
	return out
}

// ── measurement ─────────────────────────────────────────────────────────────

func TestWordsAreGroupedIntoLinesTopToBottom(t *testing.T) {
	pages := parse(t, bbox([]pptxcheck.Word{
		// Deliberately out of order, and with the sub-point jitter real glyphs
		// have on a shared baseline.
		word(300, 100.4, 360, 112, "world"),
		word(100, 200, 160, 212, "second"),
		word(100, 100, 180, 112, "hello"),
	}))
	if len(pages) != 1 {
		t.Fatalf("got %d pages", len(pages))
	}
	lines := pages[0].Lines
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %+v", len(lines), lines)
	}
	if got := lines[0].Text(); got != "hello world" {
		t.Errorf("first line = %q, want words in reading order", got)
	}
	if got := lines[1].Text(); got != "second" {
		t.Errorf("second line = %q", got)
	}
}

func TestPageSizeIsRead(t *testing.T) {
	pages := parse(t, bbox([]pptxcheck.Word{word(1, 1, 2, 2, "x")}))
	if pages[0].Width != 960 || pages[0].Height != 540 {
		t.Errorf("page = %vx%v, want 960x540", pages[0].Width, pages[0].Height)
	}
}

func TestParseBBoxRejectsRubbish(t *testing.T) {
	if _, err := pptxcheck.ParseBBox([]byte("<not xml")); err == nil {
		t.Error("want an error for output that is not the expected XML")
	}
}

// ── every rule must fire ────────────────────────────────────────────────────

// TestEachRuleFires is this package's selftest. A measured check that quietly
// stopped measuring would look exactly like a clean artifact -- which is how
// the defect that prompted this package shipped in the first place.
func TestEachRuleFires(t *testing.T) {
	for _, tc := range []struct {
		rule  string
		pages []byte
		d     *deck.Deck
	}{{
		// The real defect: a title's second line drawing over the subtitle.
		rule: pptxcheck.RuleOverlap,
		pages: bbox([]pptxcheck.Word{
			word(100, 240, 500, 282, "Otimização"),
			word(100, 285, 300, 324, "Grêmio"),
			word(100, 298, 600, 316, "Aumento"),
		}),
	}, {
		rule: pptxcheck.RuleOutOfBounds,
		pages: bbox([]pptxcheck.Word{
			word(100, 100, 200, 112, "inside"),
			word(900, 520, 1100, 560, "offslide"),
		}),
	}, {
		rule: pptxcheck.RuleTinyType,
		pages: bbox([]pptxcheck.Word{
			word(100, 100, 200, 112, "normal"),
			word(100, 300, 200, 304, "shrunk"),
		}),
	}, {
		rule: pptxcheck.RuleMissingText,
		pages: bbox([]pptxcheck.Word{
			word(100, 100, 200, 112, "presente"),
		}),
		d: &deck.Deck{Slides: []deck.Slide{{
			Number: 1,
			Title:  "Comparativo entre clubes brasileiros",
		}}},
	}, {
		rule:  pptxcheck.RuleSlideCount,
		pages: bbox([]pptxcheck.Word{word(1, 1, 2, 2, "only")}),
		d:     &deck.Deck{Slides: []deck.Slide{{Number: 1}, {Number: 2}}},
	}} {
		t.Run(tc.rule, func(t *testing.T) {
			pages := parse(t, tc.pages)
			got := pptxcheck.Check(pages, tc.d, allNative(len(pages)), pptxcheck.Options{})
			if rules(got)[tc.rule] == 0 {
				t.Errorf("%s did not fire; got %v", tc.rule, rules(got))
				for _, f := range got {
					t.Logf("  %s", f)
				}
			}
		})
	}
}

func TestACleanSlideProducesNothing(t *testing.T) {
	pages := parse(t, bbox([]pptxcheck.Word{
		word(60, 60, 400, 80, "Comparativo"),
		word(60, 120, 500, 140, "entre"),
	}))
	d := &deck.Deck{Slides: []deck.Slide{{Number: 1, Title: "Comparativo entre"}}}
	if got := pptxcheck.Check(pages, d, allNative(1), pptxcheck.Options{}); len(got) > 0 {
		for _, f := range got {
			t.Errorf("unexpected finding: %s", f)
		}
	}
}

// ── the cases that must NOT fire ────────────────────────────────────────────

func TestAdjacentLinesMayShareAFractionOfAPoint(t *testing.T) {
	// PDF word boxes include ascent and descent, so tight leading routinely
	// overlaps slightly without anything being drawn over anything. A rule
	// firing on that would report every dense slide.
	pages := parse(t, bbox([]pptxcheck.Word{
		word(100, 100, 300, 118, "first"),
		word(100, 117.4, 300, 135, "second"),
	}))
	if n := rules(pptxcheck.Check(pages, nil, allNative(1), pptxcheck.Options{}))[pptxcheck.RuleOverlap]; n != 0 {
		t.Errorf("sub-point interpenetration reported as %d overlap(s)", n)
	}
}

func TestAFullBleedWordMayTouchTheEdge(t *testing.T) {
	pages := parse(t, bbox([]pptxcheck.Word{word(0, 0, 960, 20, "fullbleed")}))
	if n := rules(pptxcheck.Check(pages, nil, allNative(1), pptxcheck.Options{}))[pptxcheck.RuleOutOfBounds]; n != 0 {
		t.Errorf("a word at the page edge reported as %d out-of-bounds", n)
	}
}

func TestLetterSpacedTextIsNotReportedMissing(t *testing.T) {
	// A tracked kicker extracts with the gaps between glyphs read as word
	// breaks: "PROPO STA DE PROJ E TO". Matching whole words against that
	// reported every tracked heading in the deck as missing, which is the
	// false positive that makes a rule unusable.
	pages := parse(t, bbox([]pptxcheck.Word{
		word(100, 100, 150, 112, "PROPO"),
		word(155, 100, 200, 112, "STA"),
		word(205, 100, 230, 112, "DE"),
		word(235, 100, 280, 112, "PROJ"),
		word(285, 100, 300, 112, "E"),
		word(305, 100, 330, 112, "TO"),
	}))
	d := &deck.Deck{Slides: []deck.Slide{{Number: 1, Kicker: "Proposta de projeto"}}}
	if n := rules(pptxcheck.Check(pages, d, allNative(1), pptxcheck.Options{}))[pptxcheck.RuleMissingText]; n != 0 {
		t.Errorf("letter-spaced text reported as %d missing-text finding(s)", n)
	}
}

func TestAnImageModeSlideIsExemptFromMissingText(t *testing.T) {
	// A flat picture has no extractable text, so every word would be
	// "missing" and the rule would be pure noise on exactly the slides it
	// cannot judge.
	pages := parse(t, bbox(nil))
	d := &deck.Deck{Slides: []deck.Slide{{Number: 1, Title: "Comparativo entre clubes"}}}
	if n := rules(pptxcheck.Check(pages, d, map[int]bool{1: false}, pptxcheck.Options{}))[pptxcheck.RuleMissingText]; n != 0 {
		t.Errorf("an image-mode slide reported %d missing-text finding(s)", n)
	}
}

func TestMissingTextToleratesTheOddWord(t *testing.T) {
	// One word short of many is a phrasing difference, not lost content; the
	// rule exists to catch a region that did not render at all.
	var words []pptxcheck.Word
	present := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot",
		"golf", "hotel", "india", "juliett"}
	for i, w := range present {
		y := float64(100 + i*20)
		words = append(words, word(100, y, 300, y+14, w))
	}
	d := &deck.Deck{Slides: []deck.Slide{{
		Number: 1,
		Fields: map[string]any{"items": append(toAny(present), "kilo")},
	}}}
	pages := parse(t, bbox(words))
	if n := rules(pptxcheck.Check(pages, d, allNative(1), pptxcheck.Options{}))[pptxcheck.RuleMissingText]; n != 0 {
		t.Errorf("1 missing word of 11 reported as a finding (%d)", n)
	}
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// ── running it ──────────────────────────────────────────────────────────────

func TestRunSurfacesAReaderThatWillNotOpenTheFile(t *testing.T) {
	// soffice exiting 0 without producing a PDF is how it declines a file, so
	// "no output" has to be an error and not an empty clean result.
	run := func(_ context.Context, name string, _ ...string) ([]byte, error) {
		return nil, nil
	}
	_, err := pptxcheck.Run(context.Background(), run, "deck.pptx", nil, pptxcheck.Options{})
	if err == nil {
		t.Fatal("want an error when the reader produces no PDF")
	}
	if !strings.Contains(err.Error(), "could not open") {
		t.Errorf("the error should say the reader could not open it: %v", err)
	}
}

func TestRunSurfacesAConversionFailure(t *testing.T) {
	run := func(_ context.Context, name string, _ ...string) ([]byte, error) {
		return nil, errors.New("soffice exploded")
	}
	if _, err := pptxcheck.Run(context.Background(), run, "deck.pptx", nil, pptxcheck.Options{}); err == nil {
		t.Fatal("want an error when the reader fails")
	}
}

func TestReportSaysSoWhenThereIsNothingToSay(t *testing.T) {
	got := pptxcheck.Report(nil, 34)
	if !strings.Contains(got, "ok") || !strings.Contains(got, "34") {
		t.Errorf("Report = %q", got)
	}
}

func TestReportGroupsByRule(t *testing.T) {
	got := pptxcheck.Report([]pptxcheck.Finding{
		{Slide: 1, Rule: pptxcheck.RuleOverlap, Detail: "a over b"},
		{Slide: 3, Rule: pptxcheck.RuleOverlap, Detail: "c over d"},
		{Slide: 7, Rule: pptxcheck.RuleTinyType, Detail: "6pt"},
	}, 34)
	for _, want := range []string{"slide 1", "slide 7", "pptx-overlap=2", "pptx-tiny-type=1", "3 finding(s)"} {
		if !strings.Contains(got, want) {
			t.Errorf("Report is missing %q:\n%s", want, got)
		}
	}
}

// ── the deck model's plain-text body ────────────────────────────────────────

func TestBodyTextKeepsParagraphsAndDropsMarkup(t *testing.T) {
	s := deck.Slide{Body: template.HTML(
		"<p>Durante a <em>elaboração</em> dos mapeamentos,\n coleta de dados.</p>\n" +
			"<p>Parte da meta é alcan&ccedil;ada.</p>")}
	got := s.BodyText()
	want := "Durante a elaboração dos mapeamentos, coleta de dados.\nParte da meta é alcançada."
	if got != want {
		t.Errorf("BodyText() =\n%q\nwant\n%q", got, want)
	}
}

func TestBodyTextIsEmptyForNoBody(t *testing.T) {
	if got := (deck.Slide{}).BodyText(); got != "" {
		t.Errorf("BodyText() = %q, want empty", got)
	}
}

func TestRunConvertsExtractsAndMeasures(t *testing.T) {
	// The fake reader writes a PDF where it was told to, so Run's whole
	// sequence -- convert, find the output, extract, parse, measure -- is
	// exercised with no LibreOffice involved.
	var called []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		called = append(called, name)
		switch name {
		case "soffice":
			for i, a := range args {
				if a == "--outdir" && i+1 < len(args) {
					return nil, os.WriteFile(filepath.Join(args[i+1], "deck.pdf"), []byte("%PDF-1.4"), 0o600)
				}
			}
			return nil, errors.New("the fake reader was not told where to write")
		case "pdftotext":
			return bbox([]pptxcheck.Word{
				word(100, 240, 500, 282, "Otimização"),
				word(100, 285, 300, 324, "Grêmio"),
				word(100, 298, 600, 316, "Aumento"),
			}), nil
		}
		return nil, fmt.Errorf("unexpected command %q", name)
	}

	findings, err := pptxcheck.Run(context.Background(), run, writeMinimalPPTX(t, true), nil, pptxcheck.Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(called) != 2 || called[0] != "soffice" || called[1] != "pdftotext" {
		t.Errorf("commands = %v, want soffice then pdftotext", called)
	}
	if rules(findings)[pptxcheck.RuleOverlap] == 0 {
		t.Errorf("the overlap in the faked render was not reported: %v", rules(findings))
	}
}

// writeMinimalPPTX makes a package with one slide, native or image mode, which
// is all NativeSlides reads.
func writeMinimalPPTX(t *testing.T, native bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "deck.pptx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	z := zip.NewWriter(f)
	body := `<p:sld><p:cSld><p:spTree><p:pic/></p:spTree></p:cSld></p:sld>`
	if native {
		body = `<p:sld><p:cSld><p:spTree><p:sp><p:nvSpPr><p:cNvSpPr txBox="1"/></p:nvSpPr></p:sp></p:spTree></p:cSld></p:sld>`
	}
	w, err := z.Create("ppt/slides/slide1.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNativeSlidesDistinguishesEditableFromFlat(t *testing.T) {
	// This is what makes missing-text meaningful: the rule has to know which
	// slides could possibly have extractable text.
	for _, tc := range []struct{ native, want bool }{{true, true}, {false, false}} {
		got, err := pptxcheck.NativeSlides(writeMinimalPPTX(t, tc.native))
		if err != nil {
			t.Fatalf("NativeSlides: %v", err)
		}
		if got[1] != tc.want {
			t.Errorf("native=%v reported as %v", tc.native, got[1])
		}
	}
}

func TestNativeSlidesReportsAnUnreadableFile(t *testing.T) {
	if _, err := pptxcheck.NativeSlides(filepath.Join(t.TempDir(), "absent.pptx")); err == nil {
		t.Error("want an error for a file that does not exist")
	}
}
