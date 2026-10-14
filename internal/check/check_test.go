package check_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/check"
)

// fakeBrowser records the script it was handed and replays canned findings, so
// the checker's own logic is testable without Chrome.
type fakeBrowser struct {
	script   string
	findings []check.Finding
	err      error
}

func (f *fakeBrowser) Eval(_ context.Context, _, script string, out any) error {
	f.script = script
	if f.err != nil {
		return f.err
	}
	raw, err := json.Marshal(f.findings)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func TestRunInjectsThresholdsIntoTheProbe(t *testing.T) {
	b := &fakeBrowser{}
	opts := check.Options{MinFontPx: 9, TolerancePx: 4, Fonts: []string{"Roboto"}}

	if _, err := check.Run(context.Background(), b, "file:///deck/index.html", opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The placeholders must be substituted; a leftover one would be a
	// JavaScript syntax error at evaluation time.
	for _, placeholder := range []string{"__MIN_FONT_PX__", "__TOL_PX__", "__FONTS__"} {
		if strings.Contains(b.script, placeholder) {
			t.Errorf("probe still contains the placeholder %s", placeholder)
		}
	}
	for _, want := range []string{"const TOL = 4;", "const MIN_FONT_PX = 9;", `["Roboto"]`} {
		if !strings.Contains(b.script, want) {
			t.Errorf("probe is missing %q", want)
		}
	}
}

func TestRunAppliesDefaults(t *testing.T) {
	b := &fakeBrowser{}
	if _, err := check.Run(context.Background(), b, "file:///x", check.Options{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(b.script, "const TOL = 2.5;") {
		t.Error("default tolerance was not applied")
	}
	if !strings.Contains(b.script, "const MIN_FONT_PX = 11;") {
		t.Error("default minimum font size was not applied")
	}
	// A nil font list must still produce valid JS.
	if !strings.Contains(b.script, "const FONTS = null;") {
		t.Error("nil fonts did not serialise to valid JavaScript")
	}
}

func TestRunOrdersFindingsDeterministically(t *testing.T) {
	b := &fakeBrowser{findings: []check.Finding{
		{Slide: 3, Rule: "overflow"},
		{Slide: 1, Rule: "tiny-type"},
		{Slide: 0, Rule: "font-fallback"},
		{Slide: 1, Rule: "contrast"},
	}}

	got, err := check.Run(context.Background(), b, "file:///x", check.Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var order []string
	for _, f := range got {
		order = append(order, f.Rule)
	}
	want := "font-fallback,contrast,tiny-type,overflow"
	if strings.Join(order, ",") != want {
		t.Errorf("order = %v, want %s (slide then rule)", order, want)
	}
}

func TestRunSurfacesBrowserFailure(t *testing.T) {
	b := &fakeBrowser{err: errors.New("chrome died")}
	_, err := check.Run(context.Background(), b, "file:///x", check.Options{})
	if err == nil || !strings.Contains(err.Error(), "chrome died") {
		t.Fatalf("want the browser error surfaced, got %v", err)
	}
}

func TestReport(t *testing.T) {
	t.Run("clean deck", func(t *testing.T) {
		if got := check.Report(nil); !strings.Contains(got, "no findings") {
			t.Errorf("Report(nil) = %q", got)
		}
	})

	t.Run("names the slide and the rule", func(t *testing.T) {
		got := check.Report([]check.Finding{
			{Slide: 14, Rule: "overflow", Element: "div.slide-body", Detail: "clipped: 60px"},
			{Slide: 0, Rule: "font-fallback", Element: "Roboto", Detail: "not loaded"},
		})
		for _, want := range []string{"slide 14", "overflow", "div.slide-body", "clipped: 60px", "2 finding(s)"} {
			if !strings.Contains(got, want) {
				t.Errorf("report is missing %q:\n%s", want, got)
			}
		}
		// A deck-level finding has no slide number and must not read "slide 0".
		if strings.Contains(got, "slide 0") {
			t.Errorf("deck-level finding rendered as slide 0:\n%s", got)
		}
	})
}

func TestRules(t *testing.T) {
	got := check.Rules([]check.Finding{
		{Rule: "overflow"}, {Rule: "contrast"}, {Rule: "overflow"},
	})
	if strings.Join(got, ",") != "contrast,overflow" {
		t.Errorf("Rules() = %v, want the distinct rules sorted", got)
	}
}
