package parity

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
)

// DefaultMissingWordTolerance is the fraction of a slide's own significant
// words allowed to be absent from its rendered PNG's OCR text before the
// slide is a hard failure rather than an advisory note.
//
// Deliberately coarse. OCR against anti-aliased screen text drops a
// scattered few percent of individual words even on a perfectly faithful
// render — tight kerning, a ligature, a word split across a line break — and
// a slide's own chrome (kicker repeated near a section title, a club name
// in both a caption and a table) can make the true denominator smaller than
// the naive count. What this threshold exists to catch is a dropped section
// or paragraph: many of a slide's own words missing at once, not a single
// short phrase buried among fifty others on an otherwise text-rich slide —
// that class is what the full missing-word list in Report is for, read by a
// human the same way an adversarial pass already does by hand.
const DefaultMissingWordTolerance = 0.12

// SlideResult is one slide's OCR comparison outcome.
type SlideResult struct {
	Number   int
	Source   string
	Expected int
	// Missing is every expected word the OCR transcript did not contain,
	// sorted, so the report is stable across runs.
	Missing []string
}

// MissingRatio is the fraction of a slide's expected words that did not
// appear in its OCR transcript. Zero when the slide declared no significant
// words at all — nothing to find means nothing missing, not a failure.
func (r SlideResult) MissingRatio() float64 {
	if r.Expected == 0 {
		return 0
	}
	return float64(len(r.Missing)) / float64(r.Expected)
}

// Mismatch reports whether this slide fails tolerance.
func (r SlideResult) Mismatch(tolerance float64) bool {
	return r.MissingRatio() > tolerance
}

// CompareDeck OCRs each slide's rendered PNG under dir — named NN.png, the
// convention internal/engine's PNG renderer uses — and compares the
// recognised text against the slide's own ExpectedWords. Slides with
// Skip:true are never in d.Slides to begin with (deck.Load already dropped
// them), so there is nothing to special-case here.
func CompareDeck(ctx context.Context, run Runner, d *deck.Deck, dir, lang string) ([]SlideResult, error) {
	results := make([]SlideResult, 0, len(d.Slides))
	for _, s := range d.Slides {
		path := filepath.Join(dir, fmt.Sprintf("%02d.png", s.Number))
		png, err := os.ReadFile(path) //nolint:gosec // dir is an operator-supplied flag, same trust boundary as -dir elsewhere
		if err != nil {
			return nil, fmt.Errorf("slide %d (%s): %w", s.Number, s.Source, err)
		}
		text, err := run(ctx, png, lang)
		if err != nil {
			return nil, fmt.Errorf("slide %d (%s): %w", s.Number, s.Source, err)
		}
		results = append(results, compareOne(s, text))
	}
	return results, nil
}

func compareOne(s deck.Slide, ocrText string) SlideResult {
	expected := ExpectedWords(s)
	found := significantWords(normalize(ocrText))

	var missing []string
	for w := range expected {
		if !found[w] {
			missing = append(missing, w)
		}
	}
	sort.Strings(missing)

	return SlideResult{Number: s.Number, Source: s.Source, Expected: len(expected), Missing: missing}
}

// HasMismatch reports whether any result fails tolerance.
func HasMismatch(results []SlideResult, tolerance float64) bool {
	for _, r := range results {
		if r.Mismatch(tolerance) {
			return true
		}
	}
	return false
}

// Report renders results as a human-readable summary, one line per slide
// plus its full missing-word list, mirroring internal/golden's Report so the
// two read alike. The missing-word list is printed for every slide that has
// one, not only the ones that fail tolerance: a slide under the threshold
// can still be hiding the one short phrase a human would want to see, the
// same way (a confirmar) is two words out of sixty on an otherwise
// text-heavy slide.
func Report(results []SlideResult, tolerance float64) string {
	var b strings.Builder
	failed := 0
	for _, r := range results {
		if r.Mismatch(tolerance) {
			failed++
		}
		b.WriteString(resultLine(r, tolerance))
		b.WriteString("\n")
		if len(r.Missing) > 0 {
			fmt.Fprintf(&b, "  missing: %s\n", strings.Join(r.Missing, ", "))
		}
	}
	if failed == 0 {
		fmt.Fprintf(&b, "\nparity: ok — %d slide(s) within tolerance %.0f%%\n", len(results), tolerance*100)
		return b.String()
	}
	fmt.Fprintf(&b, "\nparity: %d of %d slide(s) exceed tolerance %.0f%%\n", failed, len(results), tolerance*100)
	return b.String()
}

func resultLine(r SlideResult, tolerance float64) string {
	status := "ok"
	if r.Mismatch(tolerance) {
		status = "FAIL"
	}
	return fmt.Sprintf("slide %d: %s — %d/%d word(s) missing (%.1f%%), tolerance %.0f%%",
		r.Number, status, len(r.Missing), r.Expected, r.MissingRatio()*100, tolerance*100)
}
