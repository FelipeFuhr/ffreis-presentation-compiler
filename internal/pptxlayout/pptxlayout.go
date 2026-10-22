// Declarative PPTX layouts: how a theme says what its slides look like as
// native PowerPoint objects.
//
// The HTML export gets its geometry from CSS, measured by a browser. A native
// PPTX cannot: a real a:tbl is not a measured box, and PowerPoint does the
// layout itself. So a theme that wants editable output describes each layout a
// second time, in PPTX terms, as `<theme>/pptx/<layout>.yaml`.
//
// That is a real cost -- two descriptions of one design, which can drift --
// and it is why image mode still exists. It is also why this is declarative
// rather than Go: layouts are theme vocabulary, not compiler vocabulary. A
// compiler that hardcoded `cover` and `bullets` would stop being
// consumer-agnostic, which is exactly what the -theme-root discipline exists
// to prevent.
//
// A layout with no spec is not an error. The caller falls back to that slide's
// rendered image, so a deck can be partly editable while specs are still being
// written -- which is what makes the feature usable before all of them are.
package pptxlayout

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine/pptx"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/pathsafe"
)

// Dir is the subdirectory of a theme holding its PPTX layouts.
const Dir = "pptx"

// Style is the run formatting a shape's text carries. Deliberately small: this
// describes a layout, it does not reimplement CSS.
type Style struct {
	Size    float64 `yaml:"size"` // points
	Bold    bool    `yaml:"bold"`
	Italic  bool    `yaml:"italic"`
	Color   string  `yaml:"color"` // a theme token name, or #RRGGBB
	Align   string  `yaml:"align"` // left | center | right
	Caps    bool    `yaml:"caps"`
	Spacing float64 `yaml:"spacing"` // letter spacing, points
	Font    string  `yaml:"font"`
	Line    float64 `yaml:"line"` // line spacing, percent
}

// ShapeSpec is one shape in a layout.
type ShapeSpec struct {
	Kind string `yaml:"kind"` // text | bullets | table | picture | rect

	// From names where the content comes from: a chrome key (kicker, title,
	// phase, badge) or a layout field. Text may instead be literal.
	From string `yaml:"from"`
	Text string `yaml:"text"`

	// Table sources. Default to the conventional field names so a spec only
	// states them when a layout is unusual.
	Columns string `yaml:"columns"`
	Rows    string `yaml:"rows"`

	// Asset places an image from the THEME's own directory rather than from a
	// deck field -- a logo lockup, a watermark. The HTML templates hardcode
	// these, so without it a spec cannot reproduce them.
	Asset string `yaml:"asset"`

	// When names a field that must be truthy for this shape to appear, which
	// is how a template's `{{ with get .Fields "logo" }}` is expressed here.
	When string `yaml:"when"`

	// GrowTo is a CSS selector whose bottom this shape's box extends down to,
	// when that is lower than its own.
	//
	// A measured box fits the text the way CSS broke it. A reader re-breaks it
	// and may need another line or two, and with normAutofit the shortfall is
	// paid in type size -- newly mapped body prose came out at 6pt, below any
	// legibility floor. Where the layout has already reserved space beneath an
	// element, claiming it lets the text stay readable instead.
	//
	// Bounded by construction: the box can only reach the named element's
	// bottom, so it cannot grow into whatever sits below that.
	GrowTo string `yaml:"grow_to"`

	// Measure is a CSS selector naming the element that renders this shape in
	// the HTML. When set, the shape's rectangle and formatting come from the
	// browser's own layout of that element, and `at` is not needed.
	//
	// This is the preferred form. Hand-written geometry cannot know where a
	// line of text ends or how tall a table row is, and the first version of
	// these specs was 14-18% off on tables for exactly that reason.
	Measure string `yaml:"measure"`

	// At is [x, y, width, height] in inches from the slide's top-left.
	//
	// A zero width or height on a picture is CSS's `auto`: it is derived from
	// the image's own pixels so the aspect ratio is preserved. Forcing both
	// squashed the one non-square crest in the first theme to use this.
	At []float64 `yaml:"at"`

	Fill      string  `yaml:"fill"`
	Line      string  `yaml:"line"`
	LineWidth float64 `yaml:"line_width"`
	Bullet    string  `yaml:"bullet"`

	Style Style `yaml:"style"`

	// Required makes a missing source a build error. The default is to skip
	// the shape, because most layouts have optional regions -- a slide with no
	// badge should not fail, it should have no badge.
	Required bool `yaml:"required"`
}

// Spec is one layout's PPTX description.
type Spec struct {
	// Background fills the slide before anything else. A token or #RRGGBB.
	Background string      `yaml:"background"`
	Shapes     []ShapeSpec `yaml:"shapes"`
}

// Load reads every <themeDir>/pptx/<layout>.yaml. A theme with no such
// directory returns an empty map and no error: describing layouts in PPTX
// terms is opt-in.
func Load(themeDir string) (map[string]Spec, error) {
	dir := filepath.Join(themeDir, Dir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return map[string]Spec{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("pptxlayout: read %s: %w", dir, err)
	}

	specs := map[string]Spec{}
	for _, e := range entries {
		name := e.Name()
		ext := filepath.Ext(name)
		if e.IsDir() || (ext != ".yaml" && ext != ".yml") {
			continue
		}
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path) //nolint:gosec // inside the operator-supplied theme root
		if err != nil {
			return nil, fmt.Errorf("pptxlayout: read %s: %w", path, err)
		}
		var spec Spec
		if err := yaml.Unmarshal(raw, &spec); err != nil {
			return nil, fmt.Errorf("pptxlayout: %s: %w", path, err)
		}
		specs[strings.TrimSuffix(name, ext)] = spec
	}
	return specs, nil
}

// Names lists the described layouts, sorted, for doctor-style reporting.
func Names(specs map[string]Spec) []string {
	out := make([]string, 0, len(specs))
	for name := range specs {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ── colour ──────────────────────────────────────────────────────────────────

var hexColour = regexp.MustCompile(`^#?([0-9a-fA-F]{6})$`)
var shortHex = regexp.MustCompile(`^#?([0-9a-fA-F]{3})$`)

// Colour resolves a token name or literal to "RRGGBB", or "" when it cannot be
// represented.
//
// Returning "" rather than erroring is deliberate: a theme's palette contains
// rgba() and colour functions that a PPTX solid fill has no equivalent for
// (the same reason the d2 generator drops them). A shape then renders with no
// fill, which is visible and correctable, where aborting the build over a
// translucent hairline would not be.
func Colour(tokens map[string]string, value string) string {
	seen := map[string]bool{}
	v := strings.TrimSpace(value)
	for v != "" {
		if m := hexColour.FindStringSubmatch(v); m != nil {
			return strings.ToUpper(m[1])
		}
		if m := shortHex.FindStringSubmatch(v); m != nil {
			// #abc -> #aabbcc
			var b strings.Builder
			for _, c := range m[1] {
				b.WriteRune(c)
				b.WriteRune(c)
			}
			return strings.ToUpper(b.String())
		}
		if seen[v] {
			return "" // a token cycle; no colour rather than a hang
		}
		seen[v] = true
		next, ok := tokens[strings.TrimPrefix(v, "--")]
		if !ok {
			return ""
		}
		v = strings.TrimSpace(next)
	}
	return ""
}

// ── field access ────────────────────────────────────────────────────────────

// Intrinsics a spec can name beyond the slide's own front matter. Prefixed, so
// they can never collide with a layout field a theme invents.
const (
	IntrinsicSlideNumber = "slide.number"
	IntrinsicSlideBody   = "slide.body"
	IntrinsicDeckTitle   = "deck.title"
	IntrinsicDeckSub     = "deck.subtitle"
	IntrinsicDeckClient  = "deck.client"
	IntrinsicDeckFooter  = "deck.footer"
)

// resolve reads whatever a shape's `from` names: an intrinsic, slide chrome,
// or a layout field.
func resolve(key string, s deck.Slide, d *deck.Deck) (any, bool) {
	switch key {
	case IntrinsicSlideNumber:
		return s.Number, s.Number > 0
	case IntrinsicSlideBody:
		// The Markdown body as characters rather than rendered HTML. Without
		// this a layout's framing prose never reached a native slide, and six
		// slides in the first consumer deck lost theirs silently.
		text := s.BodyText()
		return text, strings.TrimSpace(text) != ""
	case IntrinsicDeckTitle:
		return deckField(d, func(d *deck.Deck) string { return d.Title })
	case IntrinsicDeckSub:
		return deckField(d, func(d *deck.Deck) string { return d.Subtitle })
	case IntrinsicDeckClient:
		return deckField(d, func(d *deck.Deck) string { return d.Client })
	case IntrinsicDeckFooter:
		return deckField(d, func(d *deck.Deck) string { return d.Footer })
	}
	return Value(s, key)
}

func deckField(d *deck.Deck, get func(*deck.Deck) string) (any, bool) {
	if d == nil {
		return nil, false
	}
	v := get(d)
	return v, strings.TrimSpace(v) != ""
}

// Value reads a chrome key or a layout field from a slide.
//
// Chrome first, and by the same names the front-matter uses, so a spec reads
// the way a slide is written.
func Value(s deck.Slide, key string) (any, bool) {
	switch key {
	case "":
		return nil, false
	case "kicker":
		return s.Kicker, s.Kicker != ""
	case "title":
		return s.Title, s.Title != ""
	case "phase":
		return s.Phase, s.Phase != ""
	case "badge":
		return s.Badge, s.Badge != ""
	}
	v, ok := s.Fields[key]
	return v, ok && v != nil
}

// text coerces a scalar to a string. Numbers appear when a deck writes an
// unquoted figure, so they are formatted rather than rejected.
func text(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, strings.TrimSpace(x) != ""
	case int:
		return strconv.Itoa(x), true
	case int64:
		return strconv.FormatInt(x, 10), true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(x), true
	case nil:
		return "", false
	default:
		return fmt.Sprint(x), true
	}
}

func stringList(v any) ([]string, bool) {
	items, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, _ := text(item)
		out = append(out, s)
	}
	return out, true
}

func stringGrid(v any) ([][]string, bool) {
	rows, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([][]string, 0, len(rows))
	for _, row := range rows {
		cells, ok := stringList(row)
		if !ok {
			// A row shaped as a map belongs to a layout whose template reads
			// it structurally; this mapper has no column order for it.
			return nil, false
		}
		out = append(out, cells)
	}
	return out, true
}

// ── mapping a slide onto a spec ─────────────────────────────────────────────

// Options is what the mapper needs beyond the slide and its spec.
type Options struct {
	// DeckRoot resolves a picture's relative path. Containment is enforced,
	// the same as every other deck-author-supplied path in this compiler.
	DeckRoot string
	// Tokens is the theme's palette, for resolving colour names.
	Tokens map[string]string
	// ThemeDir resolves an Asset. Contained, like DeckRoot.
	ThemeDir string
	// Font, when set, is the typeface every run uses, overriding whatever the
	// render measured. A theme sets it when its CSS face is not one the
	// reader's machine can be assumed to have: OOXML names one face with no
	// fallback, so that choice has to be made deliberately rather than
	// inherited from a CSS stack the browser resolved locally.
	Font string
	// Deck resolves the `deck.*` intrinsics below. A layout's chrome is not
	// all per-slide -- the footer text and the cover's title live on the deck
	// -- and a spec that could not reach them would leave a visible hole.
	Deck *deck.Deck
	// Measured is this slide's geometry as the browser laid it out. Empty
	// means nothing was measured, and every shape falls back to its spec's
	// own `at` -- which is what happens when the deck is built without a
	// browser, or for a theme whose specs state geometry by hand.
	Measured SlideMeasurement
	// Size is the slide size, used for a background that fills it.
	WidthIn, HeightIn float64
}

// Shapes maps one slide onto its layout's spec.
//
// Returns the shapes and a list of notes: a shape skipped because its source
// is absent is normal (an optional region), but worth reporting so a build
// says what it left out rather than silently producing a sparse slide.
func Shapes(spec Spec, s deck.Slide, opt Options) ([]pptx.Shape, []string, error) {
	var shapes []pptx.Shape
	var notes []string

	if bg := Colour(opt.Tokens, spec.Background); bg != "" {
		shapes = append(shapes, pptx.Shape{
			Kind: pptx.ShapeRect,
			Name: "Background",
			At:   pptx.Rect{WIn: opt.WidthIn, HIn: opt.HeightIn},
			Fill: bg,
		})
	}

	for i, sp := range spec.Shapes {
		if sp.When != "" {
			if _, on := resolve(sp.When, s, opt.Deck); !on {
				continue
			}
		}
		shape, ok, err := one(sp, s, opt)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: shape %d (%s): %w", s.Source, i+1, sp.Kind, err)
		}
		if !ok {
			if sp.Required {
				return nil, nil, fmt.Errorf("%s: shape %d (%s) requires %q, which the slide does not provide",
					s.Source, i+1, sp.Kind, sp.From)
			}
			notes = append(notes, fmt.Sprintf("slide %d: no %q, so its %s was skipped", s.Number, sp.From, sp.Kind))
			continue
		}
		shapes = append(shapes, shape)
	}
	return shapes, notes, nil
}

func rect(at []float64) pptx.Rect {
	var r pptx.Rect
	if len(at) > 0 {
		r.XIn = at[0]
	}
	if len(at) > 1 {
		r.YIn = at[1]
	}
	if len(at) > 2 {
		r.WIn = at[2]
	}
	if len(at) > 3 {
		r.HIn = at[3]
	}
	return r
}

// specStyle converts a spec's declared formatting, resolving colour tokens.
func specStyle(st Style, tokens map[string]string) pptx.TextStyle {
	align := ""
	switch strings.ToLower(st.Align) {
	case "center", "centre", "ctr":
		align = pptx.AlignCenter
	case "right", "r":
		align = pptx.AlignRight
	case "left", "l":
		align = pptx.AlignLeft
	}
	return pptx.TextStyle{
		SizePt:    st.Size,
		Bold:      st.Bold,
		Italic:    st.Italic,
		Color:     Colour(tokens, st.Color),
		Align:     align,
		Caps:      st.Caps,
		SpacingPt: st.Spacing,
		Font:      st.Font,
		LinePct:   st.Line,
	}
}

// one builds a single shape. ok is false when the slide has nothing to put in
// it, which is how an optional region is skipped.
func one(sp ShapeSpec, s deck.Slide, opt Options) (pptx.Shape, bool, error) {
	base := pptx.Shape{
		At:          rect(sp.At),
		Style:       specStyle(sp.Style, opt.Tokens),
		Fill:        Colour(opt.Tokens, sp.Fill),
		Line:        Colour(opt.Tokens, sp.Line),
		LineWidthPt: sp.LineWidth,
		Bullet:      sp.Bullet,
		Name:        sp.From,
	}

	// Measured geometry replaces the spec's, and brings the computed
	// formatting with it. An absent element is an unpainted region, which is
	// the same answer as an absent field: skip the shape.
	var grid *Grid
	measured := false
	if sp.Measure != "" {
		el, found := opt.Measured.Elements[sp.Measure]
		if !found {
			return base, false, nil
		}
		scale := opt.Measured.scale(opt.WidthIn)
		if scale <= 0 {
			return base, false, fmt.Errorf("measurement for %q reports no frame width", sp.Measure)
		}
		base.At = el.rect(scale)
		if sp.GrowTo != "" {
			if into, found := opt.Measured.Elements[sp.GrowTo]; found {
				bottom := into.rect(scale)
				if h := bottom.YIn + bottom.HIn - base.At.YIn; h > base.At.HIn {
					base.At.HIn = h
				}
			}
		}
		base.Style = specStyle(override(sp.Style, measuredStyle(el)), opt.Tokens)
		grid = el.Grid
		measured = true
	}

	// The deck-wide typeface applies to every shape, measured or not. It was
	// first applied only on the measured path, which left a spec written with
	// absolute geometry naming no typeface at all -- the runs then resolved
	// against PowerPoint's own default rather than the theme's.
	if opt.Font != "" {
		base.Style.Font = opt.Font
	}

	switch strings.ToLower(sp.Kind) {
	case "rect":
		base.Kind = pptx.ShapeRect
		base.Name = "Rectangle"
		return base, true, nil

	case "text":
		base.Kind = pptx.ShapeText
		if sp.Text != "" {
			base.Text = sp.Text
			return base, true, nil
		}
		v, ok := resolve(sp.From, s, opt.Deck)
		if !ok {
			return base, false, nil
		}
		str, ok := text(v)
		if !ok {
			return base, false, nil
		}
		base.Text = str
		return base, true, nil

	case "bullets":
		base.Kind = pptx.ShapeBullets
		v, ok := resolve(sp.From, s, opt.Deck)
		if !ok {
			return base, false, nil
		}
		items, ok := stringList(v)
		if !ok || len(items) == 0 {
			return base, false, nil
		}
		base.Items = items
		return base, true, nil

	case "table":
		base.Kind = pptx.ShapeTable
		base.Name = "Table"
		colKey, rowKey := sp.Columns, sp.Rows
		if colKey == "" {
			colKey = "columns"
		}
		if rowKey == "" {
			rowKey = "rows"
		}
		rowsVal, ok := resolve(rowKey, s, opt.Deck)
		if !ok {
			return base, false, nil
		}
		cells, ok := stringGrid(rowsVal)
		if !ok {
			// Rows shaped as maps rather than lists: the HTML template reads
			// them structurally and this mapper has no column order to use.
			// Falling back to the slide's image is better than a wrong table.
			return base, false, nil
		}
		base.Rows = cells
		if cols, ok := resolve(colKey, s, opt.Deck); ok {
			if list, ok := stringList(cols); ok {
				base.Columns = list
			}
		}
		if grid != nil {
			// Only when the counts agree. A measured table whose row count
			// differs from the model's means the selector found a different
			// table than the one being mapped, and using its heights would
			// misplace every row -- even division is the safer wrong answer.
			if len(grid.Rows) == len(base.Rows)+headerRows(base.Columns) {
				base.RowHeightsIn = scaled(grid.Rows, opt.Measured.scale(opt.WidthIn))
			}
			if len(grid.Cols) == len(base.Columns) || len(base.Columns) == 0 {
				base.ColWidthsIn = scaled(grid.Cols, opt.Measured.scale(opt.WidthIn))
			}
		}
		return base, true, nil

	case "picture":
		base.Kind = pptx.ShapePicture
		root, rel := opt.DeckRoot, ""
		if sp.Asset != "" {
			root, rel = opt.ThemeDir, sp.Asset
		} else {
			v, ok := resolve(sp.From, s, opt.Deck)
			if !ok {
				return base, false, nil
			}
			if rel, ok = text(v); !ok {
				return base, false, nil
			}
		}
		abs, err := pathsafe.Contain(root, rel)
		if err != nil {
			return base, false, fmt.Errorf("picture %q: %w", rel, err)
		}
		body, err := os.ReadFile(abs) //nolint:gosec // contained within the deck or theme root, checked above
		if err != nil {
			return base, false, fmt.Errorf("picture %q: %w", rel, err)
		}
		base.PNG = body
		base.Name = filepath.Base(abs)
		if !measured {
			// A measured rect already has the right aspect, because the
			// browser applied `width: auto` itself.
			base.At, err = fitAspect(base.At, body)
			if err != nil {
				return base, false, fmt.Errorf("picture %q: %w", rel, err)
			}
		}
		return base, true, nil

	default:
		return base, false, fmt.Errorf("unknown shape kind %q", sp.Kind)
	}
}

// headerRows is 1 when a table has a header row, 0 otherwise -- the offset
// between the model's body rows and the measured row list.
func headerRows(columns []string) int {
	if len(columns) > 0 {
		return 1
	}
	return 0
}

// scaled converts measured pixels to inches.
func scaled(px []float64, scale float64) []float64 {
	out := make([]float64, len(px))
	for i, v := range px {
		out[i] = v * scale
	}
	return out
}

// fitAspect derives a missing width or height from the image's own pixels.
//
// CSS writes `height: 2.75rem; width: auto`, and a spec that had to state both
// would distort any image whose aspect ratio it got wrong -- which is exactly
// what happened to the one non-square crest the first theme used. A zero means
// auto; stating both is still allowed, for a picture meant to be stretched.
func fitAspect(r pptx.Rect, png []byte) (pptx.Rect, error) {
	if r.WIn > 0 && r.HIn > 0 {
		return r, nil
	}
	w, h, err := pngSize(png)
	if err != nil {
		return r, err
	}
	aspect := float64(w) / float64(h)
	switch {
	case r.HIn > 0:
		r.WIn = r.HIn * aspect
	case r.WIn > 0:
		r.HIn = r.WIn / aspect
	default:
		return r, fmt.Errorf("neither width nor height given, so there is no size to derive")
	}
	return r, nil
}

// pngSize reads IHDR. Only PNG is handled because that is the only raster the
// compiler emits or embeds.
func pngSize(body []byte) (int, int, error) {
	const ihdr = 16 // 8-byte signature + 4-byte length + 4-byte type
	if len(body) < ihdr+8 || string(body[1:4]) != "PNG" {
		return 0, 0, fmt.Errorf("not a PNG")
	}
	w := int(binary.BigEndian.Uint32(body[ihdr : ihdr+4]))
	h := int(binary.BigEndian.Uint32(body[ihdr+4 : ihdr+8]))
	if w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("PNG reports a degenerate size %dx%d", w, h)
	}
	return w, h, nil
}

// ── mode ────────────────────────────────────────────────────────────────────

// Mode selects what a .pptx slide is made of.
type Mode string

const (
	// ModeNative emits real PowerPoint objects, so the file can be worked on.
	// The default: a deck handed to someone is usually handed over to be
	// edited, and a file that cannot be is a dead end for them.
	ModeNative Mode = "native"
	// ModeImage emits one full-bleed render per slide. Nothing is editable and
	// fidelity is exact -- the right choice when the file must look precisely
	// like the reviewed PDF.
	ModeImage Mode = "image"
)

// ParseMode validates a -pptx-mode value.
func ParseMode(v string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(v))) {
	case ModeNative, "":
		return ModeNative, nil
	case ModeImage:
		return ModeImage, nil
	default:
		return "", fmt.Errorf("unknown pptx mode %q: want %q or %q", v, ModeNative, ModeImage)
	}
}

// sortStrings keeps measure.go free of a sort import for one call.
func sortStrings(s []string) { sort.Strings(s) }
