// Measured checks over a rendered .pptx.
//
// WHY THIS EXISTS, and it is a correction of method rather than another rule.
//
// A native cover shipped with its title drawing 25.8 points on top of the
// subtitle beneath it, and every check in this repo passed. That was not bad
// luck:
//
//   - `check` measures the rendered DOM. The DOM was correct -- the overlap
//     appeared when a READER re-broke the lines inside a box sized from it.
//   - `verify` measures the package. The package was conformant; a text box
//     that overflows is perfectly well-formed OOXML.
//   - The side-by-side comparison scored that slide at 3.0% different, BETTER
//     than twenty others, because a global pixel-difference percentage is a
//     similarity score and 25.8 points of collision is a rounding error
//     against 921,600 pixels.
//
// The mistake was answering "does it look like the reference?" and treating
// the answer as "is it broken?". Those are different questions, and only the
// first had ever been asked of a .pptx.
//
// So this asks the second one, the same way `check` asks it of the DOM: render
// the artifact, measure where things actually landed, and report invariants
// that must hold whatever the reference looks like. Rendering is LibreOffice
// and the measurement is the PDF's own word boxes, which means these findings
// are *a* reader's, not PowerPoint's -- but a reader that re-flows text is
// exactly the class of reader that produced the defect, and it caught by
// measurement what a human caught by eye.
package pptxcheck

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/parity"
)

// Rule names. Prefixed, so a finding is never confused with one of `check`'s
// DOM rules in a log that carries both.
const (
	RuleOverlap     = "pptx-overlap"
	RuleOutOfBounds = "pptx-out-of-bounds"
	RuleTinyType    = "pptx-tiny-type"
	RuleMissingText = "pptx-missing-text"
	RuleSlideCount  = "pptx-slide-count"
)

// Defaults, each with a reason rather than a round number.
const (
	// DefaultOverlapTolerancePt allows a point of interpenetration. PDF word
	// boxes include ascent and descent, so adjacent lines of tight leading
	// routinely share a fraction of a point without anything being drawn over
	// anything.
	DefaultOverlapTolerancePt = 1.0

	// DefaultMinTypePt is a projection legibility floor, matching the spirit
	// of check's tiny-type rule. Measured from the line's own box, which is
	// cap height plus descent and therefore a little larger than the point
	// size -- so this is deliberately below where a real floor would sit, to
	// report only type that is unarguably too small.
	DefaultMinTypePt = 7.0

	// DefaultEdgeTolerancePt allows a word box to touch the page edge. A
	// full-bleed element legitimately starts at zero.
	DefaultEdgeTolerancePt = 0.5

	// DefaultMissingWordTolerance matches parity's: this is the same question
	// asked of extracted text rather than OCR, and the answer should not
	// change because the transcription got better.
	DefaultMissingWordTolerance = parity.DefaultMissingWordTolerance
)

// Runner shells out and returns the command's standard output.
//
// Injected for the reason engine.Runner is: every test in this package runs
// against canned output, so none of them needs LibreOffice installed to
// decide whether it passes.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Finding is one measured defect.
type Finding struct {
	Slide  int
	Rule   string
	Detail string
}

func (f Finding) String() string {
	return fmt.Sprintf("slide %d  %-20s %s", f.Slide, f.Rule, f.Detail)
}

// Options tunes the thresholds.
type Options struct {
	OverlapTolerancePt   float64
	MinTypePt            float64
	EdgeTolerancePt      float64
	MissingWordTolerance float64
}

func (o Options) withDefaults() Options {
	if o.OverlapTolerancePt == 0 {
		o.OverlapTolerancePt = DefaultOverlapTolerancePt
	}
	if o.MinTypePt == 0 {
		o.MinTypePt = DefaultMinTypePt
	}
	if o.EdgeTolerancePt == 0 {
		o.EdgeTolerancePt = DefaultEdgeTolerancePt
	}
	if o.MissingWordTolerance == 0 {
		o.MissingWordTolerance = DefaultMissingWordTolerance
	}
	return o
}

// ── the measurement ─────────────────────────────────────────────────────────

// Word is one word's box, in PDF points from the page's top-left.
type Word struct {
	X0, Y0, X1, Y1 float64
	Text           string
}

// Line is a run of words sharing a baseline.
type Line struct {
	Y0, Y1 float64
	Words  []string
}

func (l Line) Text() string { return strings.Join(l.Words, " ") }

// Page is one rendered slide.
type Page struct {
	Width, Height float64
	Words         []Word
	Lines         []Line
}

// lineBandPt is how far two words' tops may differ and still be one line.
// Smaller than any plausible leading, larger than the sub-point jitter of
// glyphs with different ascents on the same baseline.
const lineBandPt = 4.0

// pdfBBox is pdftotext -bbox's output.
type pdfBBox struct {
	Pages []struct {
		Width  float64 `xml:"width,attr"`
		Height float64 `xml:"height,attr"`
		Words  []struct {
			XMin float64 `xml:"xMin,attr"`
			YMin float64 `xml:"yMin,attr"`
			XMax float64 `xml:"xMax,attr"`
			YMax float64 `xml:"yMax,attr"`
			Text string  `xml:",chardata"`
		} `xml:"word"`
	} `xml:"body>doc>page"`
}

// ParseBBox reads pdftotext -bbox XML into pages with their words grouped
// into lines.
func ParseBBox(raw []byte) ([]Page, error) {
	var doc pdfBBox
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("pptxcheck: parse word boxes: %w", err)
	}

	pages := make([]Page, 0, len(doc.Pages))
	for _, p := range doc.Pages {
		page := Page{Width: p.Width, Height: p.Height}
		for _, w := range p.Words {
			text := strings.TrimSpace(w.Text)
			if text == "" {
				continue
			}
			page.Words = append(page.Words, Word{X0: w.XMin, Y0: w.YMin, X1: w.XMax, Y1: w.YMax, Text: text})
		}
		page.Lines = groupLines(page.Words)
		pages = append(pages, page)
	}
	return pages, nil
}

// groupLines gathers words into lines, top to bottom.
func groupLines(words []Word) []Line {
	sorted := make([]Word, len(words))
	copy(sorted, words)
	sort.SliceStable(sorted, func(i, j int) bool {
		if math.Abs(sorted[i].Y0-sorted[j].Y0) < lineBandPt {
			return sorted[i].X0 < sorted[j].X0
		}
		return sorted[i].Y0 < sorted[j].Y0
	})

	var lines []Line
	for _, w := range sorted {
		if n := len(lines); n > 0 && math.Abs(lines[n-1].Y0-w.Y0) < lineBandPt {
			lines[n-1].Y1 = math.Max(lines[n-1].Y1, w.Y1)
			lines[n-1].Words = append(lines[n-1].Words, w.Text)
			continue
		}
		lines = append(lines, Line{Y0: w.Y0, Y1: w.Y1, Words: []string{w.Text}})
	}
	return lines
}

// ── the rules ───────────────────────────────────────────────────────────────

// Check measures already-rendered pages against the deck that produced them.
//
// Separated from Run so the rules are testable without LibreOffice: every
// rule here is a pure function of pages, the deck and the set of native
// slides.
func Check(pages []Page, d *deck.Deck, native map[int]bool, opts Options) []Finding {
	opts = opts.withDefaults()
	var out []Finding

	if d != nil && len(pages) != len(d.Slides) {
		out = append(out, Finding{Rule: RuleSlideCount,
			Detail: fmt.Sprintf("the rendered file has %d slides, the deck declares %d",
				len(pages), len(d.Slides))})
	}

	for i, page := range pages {
		n := i + 1
		out = append(out, checkOverlap(n, page, opts)...)
		out = append(out, checkBounds(n, page, opts)...)
		out = append(out, checkTinyType(n, page, opts)...)
		if d != nil {
			out = append(out, checkMissingText(n, page, d, native, opts)...)
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Slide != out[j].Slide {
			return out[i].Slide < out[j].Slide
		}
		return out[i].Rule < out[j].Rule
	})
	return out
}

// checkOverlap is the rule the cover defect needed.
//
// Consecutive lines only: a non-consecutive pair cannot interpenetrate without
// the pair between them doing so first, and comparing every pair would report
// the same collision three times.
func checkOverlap(n int, page Page, opts Options) []Finding {
	var out []Finding
	for i := 0; i+1 < len(page.Lines); i++ {
		upper, lower := page.Lines[i], page.Lines[i+1]
		by := upper.Y1 - lower.Y0
		if by > opts.OverlapTolerancePt {
			out = append(out, Finding{Slide: n, Rule: RuleOverlap,
				Detail: fmt.Sprintf("%.1fpt of overlap: %q draws over %q",
					by, truncate(upper.Text(), 40), truncate(lower.Text(), 40))})
		}
	}
	return out
}

// checkBounds catches text the reader pushed off the slide.
//
// The same overflow that makes text collide can push it past the edge instead,
// where nothing collides and the text is simply gone when presented.
func checkBounds(n int, page Page, opts Options) []Finding {
	if page.Width <= 0 || page.Height <= 0 {
		return nil
	}
	t := opts.EdgeTolerancePt
	var out []Finding
	for _, w := range page.Words {
		switch {
		case w.X0 < -t, w.Y0 < -t, w.X1 > page.Width+t, w.Y1 > page.Height+t:
			out = append(out, Finding{Slide: n, Rule: RuleOutOfBounds,
				Detail: fmt.Sprintf("%q sits at (%.0f,%.0f)-(%.0f,%.0f) outside the %.0fx%.0fpt slide",
					truncate(w.Text, 30), w.X0, w.Y0, w.X1, w.Y1, page.Width, page.Height)})
		}
	}
	return out
}

// checkTinyType is a legibility floor, and the reason it is here rather than
// only in `check`: normAutofit shrinks text to fit its box, so the mechanism
// that stops a title overlapping is the same one that can shrink it past
// readable. The two rules have to travel together.
func checkTinyType(n int, page Page, opts Options) []Finding {
	var out []Finding
	worst := math.MaxFloat64
	var worstLine string
	for _, l := range page.Lines {
		if h := l.Y1 - l.Y0; h > 0 && h < opts.MinTypePt && h < worst {
			worst, worstLine = h, l.Text()
		}
	}
	if worstLine != "" {
		out = append(out, Finding{Slide: n, Rule: RuleTinyType,
			Detail: fmt.Sprintf("a line measures %.1fpt tall, below the %.1fpt floor: %q — "+
				"autofit may have shrunk it to fit", worst, opts.MinTypePt, truncate(worstLine, 40))})
	}
	return out
}

// checkMissingText asks whether the words the deck declares actually reached
// the rendered file.
//
// This is parity's question with the guesswork removed. parity OCRs a slide
// image, so it tolerates 12% of words going missing because OCR mangles text.
// A native slide carries REAL text, so the comparison is exact and a word that
// is absent is absent -- which is the only check here that would notice a
// field silently failing to map to a shape.
//
// Image-mode slides are exempt, and say so: a flat picture has no extractable
// text at all, so every word would be "missing" and the rule would be noise on
// precisely the slides it cannot judge.
func checkMissingText(n int, page Page, d *deck.Deck, native map[int]bool, opts Options) []Finding {
	if !native[n] {
		return nil
	}
	if n > len(d.Slides) {
		return nil
	}
	slide := d.Slides[n-1]

	expected := parity.ExpectedWords(slide)
	if len(expected) == 0 {
		return nil
	}
	var text strings.Builder
	for _, l := range page.Lines {
		text.WriteString(l.Text())
		text.WriteByte(' ')
	}
	got := parity.Words(text.String())

	// Letter-spaced type defeats word matching. A PDF extractor reads the gaps
	// between tracked glyphs as word breaks, so a kicker set in tracked
	// capitals arrives as "PROPO STA DE PROJ E TO" and every word in it looks
	// absent. Collapsing each line to a single run of letters and asking
	// whether the expected word appears in it recovers those without
	// weakening the rule: a word found inside a line's own letters, in order,
	// did render.
	runs := make([]string, 0, len(page.Lines))
	for _, l := range page.Lines {
		runs = append(runs, strings.ReplaceAll(parity.Fold(l.Text()), " ", ""))
	}
	rendered := func(w string) bool {
		if got[w] {
			return true
		}
		for _, run := range runs {
			if strings.Contains(run, w) {
				return true
			}
		}
		return false
	}

	var missing []string
	for w := range expected {
		if !rendered(w) {
			missing = append(missing, w)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	ratio := float64(len(missing)) / float64(len(expected))
	if ratio <= opts.MissingWordTolerance {
		return nil
	}
	return []Finding{{Slide: n, Rule: RuleMissingText,
		Detail: fmt.Sprintf("%d of %d declared words did not reach the slide (%.0f%%): %s",
			len(missing), len(expected), ratio*100, truncate(strings.Join(missing, " "), 60))}}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ── running it ──────────────────────────────────────────────────────────────

// NativeSlides reports which slides carry real text boxes rather than one
// flat image, which is what makes the missing-text rule meaningful.
func NativeSlides(pptxPath string) (map[int]bool, error) {
	zr, err := zip.OpenReader(pptxPath)
	if err != nil {
		return nil, fmt.Errorf("pptxcheck: open %s: %w", pptxPath, err)
	}
	defer func() { _ = zr.Close() }()

	slidePart := regexp.MustCompile(`^ppt/slides/slide(\d+)\.xml$`)
	out := map[int]bool{}
	for _, f := range zr.File {
		m := slidePart.FindStringSubmatch(f.Name)
		if m == nil {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("pptxcheck: read %s: %w", f.Name, err)
		}
		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return nil, fmt.Errorf("pptxcheck: read %s: %w", f.Name, err)
		}
		var n int
		if _, err := fmt.Sscanf(m[1], "%d", &n); err != nil {
			continue
		}
		text := string(body)
		out[n] = strings.Contains(text, `txBox="1"`) || strings.Contains(text, "<a:tbl>")
	}
	return out, nil
}

// Run renders pptxPath and measures it.
func Run(ctx context.Context, run Runner, pptxPath string, d *deck.Deck, opts Options) ([]Finding, error) {
	work, err := os.MkdirTemp("", "pptxcheck-*")
	if err != nil {
		return nil, fmt.Errorf("pptxcheck: temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	if _, err := run(ctx, "soffice", "--headless", "--norestore",
		"-env:UserInstallation=file://"+filepath.Join(work, "profile"),
		"--convert-to", "pdf", "--outdir", work, pptxPath); err != nil {
		return nil, fmt.Errorf("pptxcheck: convert %s: %w", pptxPath, err)
	}

	pdf := ""
	entries, err := os.ReadDir(work)
	if err != nil {
		return nil, fmt.Errorf("pptxcheck: read %s: %w", work, err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".pdf") {
			pdf = filepath.Join(work, e.Name())
			break
		}
	}
	if pdf == "" {
		// The reader declining the file is itself the finding, and a loud one.
		return nil, fmt.Errorf("pptxcheck: the reader produced no PDF from %s, so it could not open it", pptxPath)
	}

	raw, err := run(ctx, "pdftotext", "-bbox", pdf, "-")
	if err != nil {
		return nil, fmt.Errorf("pptxcheck: extract word boxes: %w", err)
	}
	pages, err := ParseBBox(raw)
	if err != nil {
		return nil, err
	}

	native, err := NativeSlides(pptxPath)
	if err != nil {
		return nil, err
	}
	return Check(pages, d, native, opts), nil
}

// Report renders findings for a human, grouped by slide.
func Report(findings []Finding, slides int) string {
	if len(findings) == 0 {
		return fmt.Sprintf("pptx-check: ok — no measured defects across %d slides\n", slides)
	}
	var b strings.Builder
	for _, f := range findings {
		fmt.Fprintf(&b, "%s\n", f)
	}
	rules := map[string]int{}
	for _, f := range findings {
		rules[f.Rule]++
	}
	names := make([]string, 0, len(rules))
	for r := range rules {
		names = append(names, r)
	}
	sort.Strings(names)
	fmt.Fprintf(&b, "\npptx-check: %d finding(s) across %d rule(s):", len(findings), len(names))
	for _, r := range names {
		fmt.Fprintf(&b, " %s=%d", r, rules[r])
	}
	b.WriteByte('\n')
	return b.String()
}
