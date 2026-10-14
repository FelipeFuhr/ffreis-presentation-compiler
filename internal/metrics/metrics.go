// Package metrics extracts per-slide layout metrics from a rendered deck —
// box geometry, text volume, bullet counts, rendered font sizes, palette
// usage, density and image count — as the input to any review, human or
// model.
//
// It is a sibling of internal/check, not an extension of it: check answers
// pass/fail questions against fixed thresholds, while metrics only measures
// and reports, with no opinion of its own. `make review` is what adds an
// opinion, by handing this package's JSON to a model — see the Makefile's
// `review` target, and internal/cli's `metrics` subcommand which has
// standalone value independent of that.
package metrics

import (
	"context"
	_ "embed"
	"fmt"
)

//go:embed metrics.js
var metricsJS string

// Browser evaluates JavaScript in a page and decodes the result. It is a
// port so this package is testable without a browser — the same shape
// internal/check.Browser uses.
type Browser interface {
	Eval(ctx context.Context, pageURL, script string, out any) error
}

// Box is one slide's direct-child element: the compiler's chrome structure
// (kicker, title, body, footer…) for a framed slide, or whatever a bare
// layout emits at its own top level.
type Box struct {
	Element string  `json:"element"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Width   float64 `json:"width"`
	Height  float64 `json:"height"`
}

// SlideMetrics is one slide's measured layout.
type SlideMetrics struct {
	Slide       int       `json:"slide"`
	Layout      string    `json:"layout"`
	Boxes       []Box     `json:"boxes"`
	TextLength  int       `json:"textLength"`
	BulletCount int       `json:"bulletCount"`
	FontSizesPx []float64 `json:"fontSizesPx"`
	Palette     []string  `json:"palette"`
	// Density is the fraction of the slide's own area covered by its text
	// leaves' bounding boxes — text area divided by slide area.
	Density    float64 `json:"density"`
	ImageCount int     `json:"imageCount"`
}

// Run evaluates the probe against pageURL and returns one SlideMetrics per
// rendered slide, in document order.
func Run(ctx context.Context, b Browser, pageURL string) ([]SlideMetrics, error) {
	var out []SlideMetrics
	if err := b.Eval(ctx, pageURL, metricsJS, &out); err != nil {
		return nil, fmt.Errorf("evaluate metrics: %w", err)
	}
	return out, nil
}
