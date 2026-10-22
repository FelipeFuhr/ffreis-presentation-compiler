package pptxlayout_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/pptxlayout"
)

// fakeBrowser answers Eval from a canned payload and records the script, so
// every test here runs with no Chrome installed -- the same discipline
// internal/check follows, and the reason engine_test.go once passed only on
// machines that happened to have a browser.
type fakeBrowser struct {
	payload string
	err     error
	script  string
	pageURL string
}

func (f *fakeBrowser) Eval(_ context.Context, pageURL, script string, out any) error {
	f.script, f.pageURL = script, pageURL
	if f.err != nil {
		return f.err
	}
	return json.Unmarshal([]byte(f.payload), out)
}

func TestMeasureInjectsTheSelectorsAndKeysBySlide(t *testing.T) {
	b := &fakeBrowser{payload: `[
	  {"slide":2,"frameW":1280,"frameH":720,"elements":{".slide-title":{"x":60,"y":91,"w":1160,"h":48}}},
	  {"slide":1,"frameW":1280,"frameH":720,"elements":{}}
	]`}

	got, err := pptxlayout.Measure(context.Background(), b, "file:///deck/index.html",
		[]string{".slide-title", ".kicker"})
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}

	if !strings.Contains(b.script, `[".slide-title",".kicker"]`) {
		t.Errorf("selectors were not injected into the probe:\n%s", firstLines(b.script, 4))
	}
	if strings.Contains(b.script, "__SELECTORS__") {
		t.Error("the placeholder survived into the script handed to the browser")
	}
	if b.pageURL != "file:///deck/index.html" {
		t.Errorf("pageURL = %q", b.pageURL)
	}

	// Keyed by the slide's own number, not by position: the probe walks the
	// DOM and a skipped slide would otherwise shift every later mapping.
	if len(got) != 2 {
		t.Fatalf("got %d slides, want 2", len(got))
	}
	if _, ok := got[2].Elements[".slide-title"]; !ok {
		t.Errorf("slide 2 lost its measured title: %+v", got[2])
	}
}

func TestMeasureSkipsTheBrowserWhenNothingIsNamed(t *testing.T) {
	// A theme whose specs state geometry by hand names no selectors, and
	// launching Chrome to measure nothing would be pure cost.
	b := &fakeBrowser{err: errors.New("the browser should not have been asked")}
	got, err := pptxlayout.Measure(context.Background(), b, "file:///x", nil)
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d measurements, want none", len(got))
	}
	if b.script != "" {
		t.Error("the browser was called despite there being no selectors")
	}
}

func TestMeasureSurfacesABrowserFailure(t *testing.T) {
	b := &fakeBrowser{err: errors.New("websocket url timeout reached")}
	if _, err := pptxlayout.Measure(context.Background(), b, "file:///x", []string{".kicker"}); err == nil {
		t.Fatal("want an error when the browser fails")
	}
}

func TestSelectorsAreDeduplicatedAndSorted(t *testing.T) {
	specs := map[string]pptxlayout.Spec{
		"table": {Shapes: []pptxlayout.ShapeSpec{
			{Measure: ".slide-title"}, {Measure: ".deck-table"}, {Measure: ".kicker"},
		}},
		"bullets": {Shapes: []pptxlayout.ShapeSpec{
			{Measure: ".slide-title"}, {Measure: ".checklist"}, {At: []float64{0, 0, 1, 1}},
		}},
	}
	got := pptxlayout.Selectors(specs)
	want := []string{".checklist", ".deck-table", ".kicker", ".slide-title"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v (stable order keeps the script diffable)", got, want)
		}
	}
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
