package metrics_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/metrics"
)

// fakeBrowser records the script it was handed and replays canned results,
// so Run's own logic is testable without Chrome — the same pattern
// internal/check's own tests use for its Browser port.
type fakeBrowser struct {
	script string
	result []metrics.SlideMetrics
	err    error
}

func (f *fakeBrowser) Eval(_ context.Context, _, script string, out any) error {
	f.script = script
	if f.err != nil {
		return f.err
	}
	raw, err := json.Marshal(f.result)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func TestRunEvaluatesTheEmbeddedProbe(t *testing.T) {
	b := &fakeBrowser{}
	if _, err := metrics.Run(context.Background(), b, "file:///deck/index.html"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// A script that does not look for rendered slides at all would mean the
	// wrong (or an empty) probe was embedded.
	if !strings.Contains(b.script, "data-slide") {
		t.Error("the embedded metrics probe was not evaluated")
	}
}

func TestRunDecodesPerSlideMetrics(t *testing.T) {
	b := &fakeBrowser{result: []metrics.SlideMetrics{
		{
			Slide: 1, Layout: "cover", TextLength: 42, BulletCount: 0, Density: 0.12, ImageCount: 1,
			FontSizesPx: []float64{16, 24}, Palette: []string{"rgb(0, 0, 0)"},
			Boxes: []metrics.Box{{Element: "h1.cover-title", Width: 800, Height: 60}},
		},
	}}

	got, err := metrics.Run(context.Background(), b, "file:///x")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d slide(s), want 1", len(got))
	}
	s := got[0]
	if s.Layout != "cover" || s.TextLength != 42 || s.Density != 0.12 {
		t.Errorf("got = %+v", s)
	}
	if len(s.Boxes) != 1 || s.Boxes[0].Element != "h1.cover-title" {
		t.Errorf("boxes = %+v", s.Boxes)
	}
}

func TestRunSurfacesBrowserFailure(t *testing.T) {
	b := &fakeBrowser{err: errors.New("chrome died")}
	_, err := metrics.Run(context.Background(), b, "file:///x")
	if err == nil || !strings.Contains(err.Error(), "chrome died") {
		t.Fatalf("want the browser error surfaced, got %v", err)
	}
}
