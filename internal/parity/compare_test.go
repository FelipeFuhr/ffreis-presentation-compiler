package parity_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/parity"
)

// writeSlidePNGs writes one placeholder PNG per slide number under dir, named
// the way internal/engine's PNG renderer names them.
func writeSlidePNGs(t *testing.T, dir string, numbers ...int) {
	t.Helper()
	for _, n := range numbers {
		path := filepath.Join(dir, fmtSlide(n))
		if err := os.WriteFile(path, []byte("<png bytes>"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
}

func fmtSlide(n int) string {
	if n < 10 {
		return "0" + itoa(n) + ".png"
	}
	return itoa(n) + ".png"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// fixedRunner returns the same OCR text for every slide, regardless of which
// PNG it was handed — enough to test the comparison logic without caring
// what bytes a placeholder PNG contains.
func fixedRunner(text string) parity.Runner {
	return func(context.Context, []byte, string) (string, error) {
		return text, nil
	}
}

func TestCompareDeckFindsNoMissingWordsWhenOCRMatchesTheSource(t *testing.T) {
	dir := t.TempDir()
	writeSlidePNGs(t, dir, 1)
	d := &deck.Deck{Slides: []deck.Slide{
		{Number: 1, Source: "01.md", Title: "Carta de Projeto", Kicker: "Definição"},
	}}

	results, err := parity.CompareDeck(context.Background(), fixedRunner("Carta de Projeto Definicao"), d, dir, "eng")
	if err != nil {
		t.Fatalf("CompareDeck: %v", err)
	}
	if len(results) != 1 || len(results[0].Missing) != 0 {
		t.Errorf("results = %+v, want slide 1 with no missing words", results)
	}
}

func TestCompareDeckReportsWordsDroppedFromTheRender(t *testing.T) {
	dir := t.TempDir()
	writeSlidePNGs(t, dir, 1)
	// The exact shape of this week's real findings: a slide's source names a
	// section ("equipe") that OCR of the actual render never shows.
	d := &deck.Deck{Slides: []deck.Slide{
		{Number: 1, Source: "19-carta-projeto.md", Fields: map[string]any{
			"items": []any{"Objetivos", "Indicadores", "Equipe"},
		}},
	}}

	results, err := parity.CompareDeck(context.Background(), fixedRunner("Objetivos Indicadores"), d, dir, "eng")
	if err != nil {
		t.Fatalf("CompareDeck: %v", err)
	}
	if len(results[0].Missing) != 1 || results[0].Missing[0] != "equipe" {
		t.Errorf("Missing = %v, want exactly [equipe]", results[0].Missing)
	}
}

func TestMismatchComparesAgainstTolerance(t *testing.T) {
	r := parity.SlideResult{Number: 1, Expected: 10, Missing: []string{"a", "b"}} // 20%
	if !r.Mismatch(0.12) {
		t.Error("20% missing must fail a 12% tolerance")
	}
	if r.Mismatch(0.25) {
		t.Error("20% missing must pass a 25% tolerance")
	}
}

func TestMissingRatioIsZeroWithNoExpectedWords(t *testing.T) {
	r := parity.SlideResult{Number: 1, Expected: 0}
	if r.MissingRatio() != 0 {
		t.Errorf("MissingRatio() = %v, want 0 when nothing was expected", r.MissingRatio())
	}
}

func TestCompareDeckFailsLoudlyOnAMissingPNG(t *testing.T) {
	dir := t.TempDir() // no PNGs written
	d := &deck.Deck{Slides: []deck.Slide{{Number: 1, Source: "01.md", Title: "Title"}}}

	_, err := parity.CompareDeck(context.Background(), fixedRunner(""), d, dir, "eng")
	if err == nil || !strings.Contains(err.Error(), "01.md") {
		t.Fatalf("err = %v, want an error naming 01.md", err)
	}
}

func TestCompareDeckPropagatesAnOCRFailure(t *testing.T) {
	dir := t.TempDir()
	writeSlidePNGs(t, dir, 1)
	d := &deck.Deck{Slides: []deck.Slide{{Number: 1, Source: "01.md", Title: "Title"}}}

	failing := func(context.Context, []byte, string) (string, error) { return "", errors.New("tesseract exploded") }
	_, err := parity.CompareDeck(context.Background(), failing, d, dir, "eng")
	if err == nil || !strings.Contains(err.Error(), "tesseract exploded") {
		t.Fatalf("err = %v, want it to wrap the OCR failure", err)
	}
}

func TestReportPrintsMissingWordsEvenUnderTolerance(t *testing.T) {
	results := []parity.SlideResult{
		{Number: 13, Source: "13-socios-afetivos.md", Expected: 60, Missing: []string{"confirmar"}},
	}
	report := parity.Report(results, parity.DefaultMissingWordTolerance)
	if !strings.Contains(report, "ok") {
		t.Errorf("one missing word out of 60 must pass the default tolerance, got %q", report)
	}
	if !strings.Contains(report, "confirmar") {
		t.Errorf("report must still list the missing word for a human to read, got %q", report)
	}
}

func TestHasMismatch(t *testing.T) {
	ok := []parity.SlideResult{{Expected: 10, Missing: nil}}
	if parity.HasMismatch(ok, parity.DefaultMissingWordTolerance) {
		t.Error("HasMismatch true with nothing missing")
	}
	bad := []parity.SlideResult{{Expected: 10, Missing: []string{"a", "b", "c"}}}
	if !parity.HasMismatch(bad, parity.DefaultMissingWordTolerance) {
		t.Error("HasMismatch false for a slide well over tolerance")
	}
}
