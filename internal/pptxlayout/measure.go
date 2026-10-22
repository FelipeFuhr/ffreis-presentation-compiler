package pptxlayout

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine/pptx"
)

//go:embed measure.js
var measureJS string

// Browser is the one capability measurement needs. Injected, for the same
// reason internal/check injects it: every test in this package runs against a
// fake, so none of them needs Chrome installed to decide whether it passes.
type Browser interface {
	Eval(ctx context.Context, pageURL, script string, out any) error
}

// pxPerPoint converts CSS pixels to PostScript points, which is what OOXML
// font sizes are in. 96 CSS px per inch, 72 points per inch.
const pxPerPoint = 96.0 / 72.0

// boldFrom is the CSS font-weight at which OOXML's b="1" is the honest
// rendering. 600 rather than 700: a theme using 600 for its titles means them
// to be bold, and PPTX has no weight axis to express the difference.
const boldFrom = 600

// Grid is a table's measured geometry: one height per row in document order,
// one width per column.
type Grid struct {
	Rows []float64 `json:"rows"`
	Cols []float64 `json:"cols"`
}

// Measurement is one element as the browser laid it out. Pixels, relative to
// its slide's frame.
type Measurement struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`

	FontSizePx      float64 `json:"fontSizePx"`
	LineHeightPx    float64 `json:"lineHeightPx"`
	LetterSpacingPx float64 `json:"letterSpacingPx"`
	Weight          int     `json:"weight"`
	Italic          bool    `json:"italic"`
	Color           string  `json:"color"`
	Align           string  `json:"align"`
	Family          string  `json:"family"`
	Transform       string  `json:"transform"`

	Grid *Grid `json:"grid"`
}

// SlideMeasurement is every named element found on one slide.
type SlideMeasurement struct {
	Slide    int                    `json:"slide"`
	FrameW   float64                `json:"frameW"`
	FrameH   float64                `json:"frameH"`
	Elements map[string]Measurement `json:"elements"`
}

// Measure reads the geometry of every selector in specs, for every slide.
//
// One browser pass for the whole deck: the page is already rendered, and
// re-evaluating per slide would pay the navigation cost 34 times over.
func Measure(ctx context.Context, b Browser, pageURL string, selectors []string) (map[int]SlideMeasurement, error) {
	if len(selectors) == 0 {
		return map[int]SlideMeasurement{}, nil
	}
	encoded, err := json.Marshal(selectors)
	if err != nil {
		return nil, fmt.Errorf("pptxlayout: encode selectors: %w", err)
	}
	script := strings.NewReplacer("__SELECTORS__", string(encoded)).Replace(measureJS)

	var slides []SlideMeasurement
	if err := b.Eval(ctx, pageURL, script, &slides); err != nil {
		return nil, fmt.Errorf("pptxlayout: measure: %w", err)
	}

	out := make(map[int]SlideMeasurement, len(slides))
	for _, s := range slides {
		out[s.Slide] = s
	}
	return out, nil
}

// Selectors is every selector the given specs name, deduplicated and sorted.
//
// Sorted so the script handed to the browser is stable: an unstable script is
// an unstable build, and this one is large enough to be worth diffing.
func Selectors(specs map[string]Spec) []string {
	seen := map[string]bool{}
	for _, spec := range specs {
		for _, sh := range spec.Shapes {
			if sh.Measure != "" {
				seen[sh.Measure] = true
			}
			// A grow target has to be measured too, or the shape silently
			// keeps its own height and the growth is a no-op.
			if sh.GrowTo != "" {
				seen[sh.GrowTo] = true
			}
		}
	}
	return sortedKeys(seen)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

// ── applying a measurement ──────────────────────────────────────────────────

// scale converts this slide's pixels to inches, derived from the frame the
// browser actually laid out rather than assumed to be 96 per inch. A deck
// rendered at any other scale measures correctly.
func (m SlideMeasurement) scale(widthIn float64) float64 {
	if m.FrameW <= 0 || widthIn <= 0 {
		return 0
	}
	return widthIn / m.FrameW
}

// rect converts a measurement to inches.
func (e Measurement) rect(scale float64) pptx.Rect {
	return pptx.Rect{
		XIn: e.X * scale,
		YIn: e.Y * scale,
		WIn: e.W * scale,
		HIn: e.H * scale,
	}
}

var rgbFunc = regexp.MustCompile(`^rgba?\(\s*([\d.]+)[,\s]+([\d.]+)[,\s]+([\d.]+)\s*(?:[,/]\s*([\d.]+)\s*)?\)$`)

// cssColour converts a computed `rgb()`/`rgba()` to "RRGGBB".
//
// A translucent colour returns "", the same answer Colour gives for a token
// whose value is rgba(): a PPTX solid fill has no alpha, and compositing it
// against an assumed backdrop would be a guess that looks like a bug.
func cssColour(v string) string {
	m := rgbFunc.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return ""
	}
	if m[4] != "" {
		if alpha, err := strconv.ParseFloat(m[4], 64); err == nil && alpha < 0.999 {
			return ""
		}
	}
	channel := func(s string) int {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || f < 0 {
			return 0
		}
		if f > 255 {
			return 255
		}
		return int(f + 0.5)
	}
	return strings.ToUpper(fmt.Sprintf("%02x%02x%02x", channel(m[1]), channel(m[2]), channel(m[3])))
}

// firstFamily is the first name in a CSS font stack, unquoted.
//
// OOXML names one typeface, with no fallback list. The first is what the
// browser preferred, so it is the honest choice -- and if it was not actually
// available, the theme's font-integrity check is what should be complaining,
// not this.
func firstFamily(stack string) string {
	for _, part := range strings.Split(stack, ",") {
		name := strings.Trim(strings.TrimSpace(part), `"'`)
		if name != "" {
			return name
		}
	}
	return ""
}

// cssAlign maps a computed text-align to OOXML's spelling. `start` and `end`
// are resolved as left and right: these decks are left-to-right, and a deck
// that is not would need more than this to be correct anyway.
func cssAlign(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "center":
		return pptx.AlignCenter
	case "right", "end":
		return pptx.AlignRight
	case "left", "start":
		return pptx.AlignLeft
	default:
		return ""
	}
}

// measuredStyle is the formatting the browser computed, as a PPTX text style.
func measuredStyle(e Measurement) Style {
	st := Style{
		Size:    e.FontSizePx / pxPerPoint,
		Bold:    e.Weight >= boldFrom,
		Italic:  e.Italic,
		Color:   "#" + cssColour(e.Color),
		Align:   cssAlign(e.Align),
		Caps:    strings.EqualFold(e.Transform, "uppercase"),
		Spacing: e.LetterSpacingPx / pxPerPoint,
		Font:    firstFamily(e.Family),
	}
	if cssColour(e.Color) == "" {
		st.Color = ""
	}
	if e.FontSizePx > 0 && e.LineHeightPx > 0 {
		st.Line = e.LineHeightPx / e.FontSizePx * 100
	}
	return st
}

// override returns the spec's value when it states one, else the measured one.
//
// This is the whole policy for reconciling the two sources: measurement is the
// default because it is what the design actually does, and an explicit entry
// in the spec wins because that is a theme author deliberately departing from
// it. Nothing is silently averaged.
func override(spec, measured Style) Style {
	out := measured
	if spec.Size != 0 {
		out.Size = spec.Size
	}
	if spec.Color != "" {
		out.Color = spec.Color
	}
	if spec.Align != "" {
		out.Align = spec.Align
	}
	if spec.Spacing != 0 {
		out.Spacing = spec.Spacing
	}
	if spec.Font != "" {
		out.Font = spec.Font
	}
	if spec.Line != 0 {
		out.Line = spec.Line
	}
	// Bold, Italic and Caps are booleans: a spec cannot say "not bold" by
	// omission, so they are ORed. A theme that needs to force a measured bold
	// off should change the CSS, which is the thing being described.
	out.Bold = measured.Bold || spec.Bold
	out.Italic = measured.Italic || spec.Italic
	out.Caps = measured.Caps || spec.Caps
	return out
}
