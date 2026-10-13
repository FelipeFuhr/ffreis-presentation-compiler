// Package check runs deterministic checks against a rendered deck.
//
// These are measurements, not opinions: the browser has already performed
// layout, so "does this text fit" is a number. That is what makes the HTML
// pipeline worth having — the defect class that shipped in hand-built decks
// (silently clipped table cells) becomes a build failure instead of something
// a human has to notice.
package check

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

//go:embed probe.js
var probeJS string

// DefaultMinFontPx is the smallest rendered type the checker tolerates. A slide
// is 1280 CSS px wide, so this is roughly 8pt — below that, projection
// legibility goes.
const DefaultMinFontPx = 11.0

// DefaultTolerancePx is how much overflow is treated as rounding rather than a
// defect. Themes are authored in inches, so nearly every box has a fractional
// pixel height (0.14in = 13.44px); a stack of them accumulates 1-3px that no
// viewer can see. Measured clipping worth acting on has been an order of
// magnitude larger — the two real bugs this checker first caught were 32px and
// 60px. Raise this only with evidence, never to make a deck pass.
const DefaultTolerancePx = 2.5

// Finding is one measured problem.
type Finding struct {
	Slide   int    `json:"slide"`
	Rule    string `json:"rule"`
	Element string `json:"element"`
	Detail  string `json:"detail"`
}

// Browser evaluates JavaScript in a page and decodes the result. It is a port
// so the checker is testable without a browser.
type Browser interface {
	Eval(ctx context.Context, pageURL, script string, out any) error
}

// Options tunes a run.
type Options struct {
	MinFontPx float64
	// TolerancePx is the overflow below which a finding is rounding noise.
	TolerancePx float64
	// Fonts the theme expects; a substituted face is reported.
	Fonts []string
}

// Run evaluates the probe against pageURL and returns findings, ordered by
// slide then rule so output is stable across runs.
func Run(ctx context.Context, b Browser, pageURL string, opts Options) ([]Finding, error) {
	minFont := opts.MinFontPx
	if minFont <= 0 {
		minFont = DefaultMinFontPx
	}
	tolerance := opts.TolerancePx
	if tolerance <= 0 {
		tolerance = DefaultTolerancePx
	}
	fonts, err := json.Marshal(opts.Fonts)
	if err != nil {
		return nil, fmt.Errorf("encode fonts: %w", err)
	}

	script := strings.NewReplacer(
		"__MIN_FONT_PX__", strconv.FormatFloat(minFont, 'f', -1, 64),
		"__TOL_PX__", strconv.FormatFloat(tolerance, 'f', -1, 64),
		"__FONTS__", string(fonts),
	).Replace(probeJS)

	var findings []Finding
	if err := b.Eval(ctx, pageURL, script, &findings); err != nil {
		return nil, fmt.Errorf("evaluate checks: %w", err)
	}

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Slide != findings[j].Slide {
			return findings[i].Slide < findings[j].Slide
		}
		return findings[i].Rule < findings[j].Rule
	})
	return findings, nil
}

// Report renders findings as a human-readable summary.
func Report(findings []Finding) string {
	if len(findings) == 0 {
		return "check: no findings\n"
	}
	var b strings.Builder
	for _, f := range findings {
		where := "deck"
		if f.Slide > 0 {
			where = fmt.Sprintf("slide %d", f.Slide)
		}
		fmt.Fprintf(&b, "%-8s %-14s %s — %s\n", where, f.Rule, f.Element, f.Detail)
	}
	fmt.Fprintf(&b, "\ncheck: %d finding(s) across %d rule(s)\n", len(findings), len(Rules(findings)))
	return b.String()
}

// Rules lists the distinct rules present in findings, sorted.
func Rules(findings []Finding) []string {
	seen := map[string]bool{}
	for _, f := range findings {
		seen[f.Rule] = true
	}
	rules := make([]string, 0, len(seen))
	for r := range seen {
		rules = append(rules, r)
	}
	sort.Strings(rules)
	return rules
}
