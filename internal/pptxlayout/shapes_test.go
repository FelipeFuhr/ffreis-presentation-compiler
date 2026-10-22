package pptxlayout_test

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine/pptx"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/pptxlayout"
)

const slideWidthIn = 13.333

// tokens is a slice of the first consumer theme's palette.
var tokens = map[string]string{
	"accent":      "#02accf",
	"ink-strong":  "#1f2d39",
	"paper":       "#fbf8f1",
	"accent-soft": "rgba(2, 172, 207, 0.1)",
}

func measuredSlide(elements map[string]pptxlayout.Measurement) pptxlayout.SlideMeasurement {
	return pptxlayout.SlideMeasurement{Slide: 1, FrameW: 1280, FrameH: 720, Elements: elements}
}

func opts(m pptxlayout.SlideMeasurement) pptxlayout.Options {
	return pptxlayout.Options{Tokens: tokens, WidthIn: slideWidthIn, HeightIn: 7.5, Measured: m}
}

func TestShapesTakeTheirGeometryFromTheMeasurement(t *testing.T) {
	spec := pptxlayout.Spec{
		Background: "paper",
		Shapes: []pptxlayout.ShapeSpec{
			{Kind: "text", From: "title", Measure: ".slide-title"},
		},
	}
	m := measuredSlide(map[string]pptxlayout.Measurement{
		".slide-title": {X: 60, Y: 96, W: 1160, H: 48, FontSizePx: 33, Weight: 700,
			Color: "rgb(31, 45, 57)", Family: "Roboto, sans-serif"},
	})

	shapes, _, err := pptxlayout.Shapes(spec, deck.Slide{Number: 1, Title: "Visão Geral"}, opts(m))
	if err != nil {
		t.Fatalf("Shapes: %v", err)
	}
	if len(shapes) != 2 {
		t.Fatalf("got %d shapes, want background + title", len(shapes))
	}

	bg := shapes[0]
	if bg.Kind != pptx.ShapeRect || bg.Fill != "FBF8F1" || bg.At.WIn != slideWidthIn {
		t.Errorf("background is wrong: %+v", bg)
	}

	title := shapes[1]
	if title.Text != "Visão Geral" {
		t.Errorf("Text = %q", title.Text)
	}
	if title.At.XIn < 0.624 || title.At.XIn > 0.626 {
		t.Errorf("XIn = %v, want ~0.625 from the measurement, not from the spec", title.At.XIn)
	}
	if !title.Style.Bold || title.Style.Color != "1F2D39" || title.Style.Font != "Roboto" {
		t.Errorf("style did not come from the measurement: %+v", title.Style)
	}
}

func TestAnUnmeasuredElementIsTreatedAsAnAbsentRegion(t *testing.T) {
	// The probe omits a zero-area element, which is what an empty optional
	// region looks like. Skipping the shape is the same answer as a missing
	// field, and the build reports it.
	spec := pptxlayout.Spec{Shapes: []pptxlayout.ShapeSpec{
		{Kind: "text", From: "conclusion", Measure: ".conclusion"},
	}}
	shapes, notes, err := pptxlayout.Shapes(spec,
		deck.Slide{Number: 1, Fields: map[string]any{"conclusion": "present in the model"}},
		opts(measuredSlide(nil)))
	if err != nil {
		t.Fatalf("Shapes: %v", err)
	}
	if len(shapes) != 0 {
		t.Errorf("got %d shapes, want none", len(shapes))
	}
	if len(notes) != 1 {
		t.Errorf("the skip should be reported, got %v", notes)
	}
}

func TestARequiredShapeFailsRatherThanVanishing(t *testing.T) {
	spec := pptxlayout.Spec{Shapes: []pptxlayout.ShapeSpec{
		{Kind: "text", From: "title", Measure: ".slide-title", Required: true},
	}}
	if _, _, err := pptxlayout.Shapes(spec, deck.Slide{Number: 1}, opts(measuredSlide(nil))); err == nil {
		t.Fatal("want an error for a required shape with nothing to put in it")
	}
}

func TestTableTakesMeasuredRowsAndColumns(t *testing.T) {
	spec := pptxlayout.Spec{Shapes: []pptxlayout.ShapeSpec{
		{Kind: "table", Fill: "accent", Measure: ".deck-table"},
	}}
	// Three measured rows for a header plus two body rows.
	m := measuredSlide(map[string]pptxlayout.Measurement{
		".deck-table": {X: 60, Y: 192, W: 1160, H: 150, FontSizePx: 16,
			Grid: &pptxlayout.Grid{Rows: []float64{48, 51, 51}, Cols: []float64{580, 580}}},
	})
	slide := deck.Slide{Number: 1, Fields: map[string]any{
		"columns": []any{"Clube", "Sócios"},
		"rows":    []any{[]any{"Clube Ômega", "9,8 milhões"}, []any{"Clube Zêta", "7,1 milhões"}},
	}}

	shapes, _, err := pptxlayout.Shapes(spec, slide, opts(m))
	if err != nil {
		t.Fatalf("Shapes: %v", err)
	}
	tbl := shapes[0]
	if len(tbl.RowHeightsIn) != 3 {
		t.Fatalf("RowHeightsIn = %v, want one per rendered row including the header", tbl.RowHeightsIn)
	}
	// Rows are content-sized, so they must NOT all be equal -- that was the
	// defect even division produced.
	if tbl.RowHeightsIn[0] == tbl.RowHeightsIn[1] {
		t.Errorf("the header row should differ from a body row: %v", tbl.RowHeightsIn)
	}
	if len(tbl.ColWidthsIn) != 2 {
		t.Errorf("ColWidthsIn = %v", tbl.ColWidthsIn)
	}
	if tbl.Fill != "02ACCF" {
		t.Errorf("header fill = %q, want the resolved accent token", tbl.Fill)
	}
}

func TestTableIgnoresAGridThatDoesNotMatchTheModel(t *testing.T) {
	// A row count that disagrees means the selector found a different table
	// than the one being mapped. Using its heights would misplace every row,
	// so even division is the safer wrong answer.
	spec := pptxlayout.Spec{Shapes: []pptxlayout.ShapeSpec{
		{Kind: "table", Measure: ".deck-table"},
	}}
	m := measuredSlide(map[string]pptxlayout.Measurement{
		".deck-table": {X: 60, Y: 192, W: 1160, H: 150,
			Grid: &pptxlayout.Grid{Rows: []float64{48, 51, 51, 51, 51}, Cols: []float64{580, 580}}},
	})
	slide := deck.Slide{Number: 1, Fields: map[string]any{
		"columns": []any{"Clube", "Sócios"},
		"rows":    []any{[]any{"Clube Ômega", "9,8"}},
	}}

	shapes, _, err := pptxlayout.Shapes(spec, slide, opts(m))
	if err != nil {
		t.Fatalf("Shapes: %v", err)
	}
	if got := shapes[0].RowHeightsIn; got != nil {
		t.Errorf("RowHeightsIn = %v, want nil so the frame is divided evenly instead", got)
	}
}

func TestRowsShapedAsMapsFallBackRatherThanGuessingColumnOrder(t *testing.T) {
	spec := pptxlayout.Spec{Shapes: []pptxlayout.ShapeSpec{{Kind: "table", Measure: ".deck-table"}}}
	m := measuredSlide(map[string]pptxlayout.Measurement{".deck-table": {X: 1, Y: 1, W: 10, H: 10}})
	slide := deck.Slide{Number: 1, Fields: map[string]any{
		"rows": []any{map[string]any{"name": "Prata", "price": "R$ 50"}},
	}}
	shapes, _, err := pptxlayout.Shapes(spec, slide, opts(m))
	if err != nil {
		t.Fatalf("Shapes: %v", err)
	}
	if len(shapes) != 0 {
		t.Errorf("got %d shapes; a map-shaped row has no column order to lay out", len(shapes))
	}
}

func TestIntrinsicsReachTheDeckAndTheSlideNumber(t *testing.T) {
	spec := pptxlayout.Spec{Shapes: []pptxlayout.ShapeSpec{
		{Kind: "text", From: "deck.title", Measure: ".cover-title"},
		{Kind: "text", From: "slide.number", Measure: ".page"},
	}}
	m := measuredSlide(map[string]pptxlayout.Measurement{
		".cover-title": {X: 60, Y: 96, W: 1000, H: 60},
		".page":        {X: 1200, Y: 680, W: 40, H: 16},
	})
	o := opts(m)
	o.Deck = &deck.Deck{Title: "Título do Deck de Exemplo"}

	shapes, _, err := pptxlayout.Shapes(spec, deck.Slide{Number: 7}, o)
	if err != nil {
		t.Fatalf("Shapes: %v", err)
	}
	if len(shapes) != 2 {
		t.Fatalf("got %d shapes, want both intrinsics resolved", len(shapes))
	}
	if shapes[0].Text != "Título do Deck de Exemplo" {
		t.Errorf("deck.title = %q", shapes[0].Text)
	}
	if shapes[1].Text != "7" {
		t.Errorf("slide.number = %q, want 7", shapes[1].Text)
	}
}

func TestIntrinsicsAreAbsentWithoutADeck(t *testing.T) {
	spec := pptxlayout.Spec{Shapes: []pptxlayout.ShapeSpec{
		{Kind: "text", From: "deck.footer", Measure: ".footer"},
	}}
	m := measuredSlide(map[string]pptxlayout.Measurement{".footer": {X: 1, Y: 1, W: 10, H: 5}})
	shapes, _, err := pptxlayout.Shapes(spec, deck.Slide{Number: 1}, opts(m))
	if err != nil {
		t.Fatalf("Shapes: %v", err)
	}
	if len(shapes) != 0 {
		t.Errorf("got %d shapes; with no deck there is no footer text", len(shapes))
	}
}

func TestWhenGatesAShapeOnAField(t *testing.T) {
	spec := pptxlayout.Spec{Shapes: []pptxlayout.ShapeSpec{
		{Kind: "rect", When: "logo", Measure: ".section-logo", Fill: "accent"},
	}}
	m := measuredSlide(map[string]pptxlayout.Measurement{".section-logo": {X: 1, Y: 1, W: 10, H: 5}})

	off, _, err := pptxlayout.Shapes(spec, deck.Slide{Number: 1}, opts(m))
	if err != nil {
		t.Fatalf("Shapes: %v", err)
	}
	if len(off) != 0 {
		t.Errorf("without the field the shape should not appear, got %d", len(off))
	}

	on, _, err := pptxlayout.Shapes(spec,
		deck.Slide{Number: 1, Fields: map[string]any{"logo": true}}, opts(m))
	if err != nil {
		t.Fatalf("Shapes: %v", err)
	}
	if len(on) != 1 {
		t.Errorf("with the field set the shape should appear, got %d", len(on))
	}
}

func TestATranslucentTokenResolvesToNoFill(t *testing.T) {
	// rgba() has no PPTX equivalent. No fill is visible and correctable;
	// compositing it against an assumed backdrop would look like a bug.
	spec := pptxlayout.Spec{Shapes: []pptxlayout.ShapeSpec{
		{Kind: "rect", Fill: "accent-soft", Measure: ".card"},
	}}
	m := measuredSlide(map[string]pptxlayout.Measurement{".card": {X: 1, Y: 1, W: 10, H: 5}})
	shapes, _, err := pptxlayout.Shapes(spec, deck.Slide{Number: 1}, opts(m))
	if err != nil {
		t.Fatalf("Shapes: %v", err)
	}
	if shapes[0].Fill != "" {
		t.Errorf("Fill = %q, want empty", shapes[0].Fill)
	}
}

func TestAnUnknownShapeKindIsAnError(t *testing.T) {
	spec := pptxlayout.Spec{Shapes: []pptxlayout.ShapeSpec{{Kind: "carousel"}}}
	if _, _, err := pptxlayout.Shapes(spec, deck.Slide{Number: 1}, opts(measuredSlide(nil))); err == nil {
		t.Fatal("want an error for a kind this mapper cannot render")
	}
}

// ── spec loading ────────────────────────────────────────────────────────────

func TestLoadReadsEveryLayoutAndIgnoresTheRest(t *testing.T) {
	dir := t.TempDir()
	pptxDir := filepath.Join(dir, pptxlayout.Dir)
	if err := os.MkdirAll(pptxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(pptxDir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("bullets.yaml", "background: paper\nshapes:\n  - {kind: text, from: title, measure: .slide-title}\n")
	write("table.yml", "shapes:\n  - {kind: table, measure: .deck-table}\n")
	write("README.md", "not a layout")

	specs, err := pptxlayout.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("got %v, want bullets and table (both .yaml and .yml, never the README)", pptxlayout.Names(specs))
	}
	if specs["bullets"].Background != "paper" {
		t.Errorf("bullets background = %q", specs["bullets"].Background)
	}
}

func TestLoadTreatsAThemeWithNoLayoutsAsOptIn(t *testing.T) {
	// Describing layouts in PPTX terms is opt-in, so a theme without the
	// directory is not an error -- it simply gets image mode.
	specs, err := pptxlayout.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(specs) != 0 {
		t.Errorf("got %d specs, want none", len(specs))
	}
}

func TestLoadSurfacesABrokenSpec(t *testing.T) {
	dir := t.TempDir()
	pptxDir := filepath.Join(dir, pptxlayout.Dir)
	if err := os.MkdirAll(pptxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pptxDir, "bad.yaml"), []byte("shapes: [ unclosed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := pptxlayout.Load(dir); err == nil {
		t.Fatal("a spec that will not parse must fail the build, not be skipped")
	}
}

func TestParseMode(t *testing.T) {
	for in, want := range map[string]pptxlayout.Mode{
		"native": pptxlayout.ModeNative,
		"image":  pptxlayout.ModeImage,
		"NATIVE": pptxlayout.ModeNative,
		" image": pptxlayout.ModeImage,
		"":       pptxlayout.ModeNative, // the default is editable
	} {
		got, err := pptxlayout.ParseMode(in)
		if err != nil {
			t.Errorf("ParseMode(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseMode(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := pptxlayout.ParseMode("editable"); err == nil {
		t.Error("an unknown mode should be a usage error, not silently the default")
	}
}

// ── pictures and aspect ratio ───────────────────────────────────────────────

// writePNG puts a w x h PNG in dir and returns its name, relative to dir.
func writePNG(t *testing.T, dir, name string, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestAPictureWithOnlyAHeightKeepsItsAspectRatio(t *testing.T) {
	// The regression this exists for: a 96x81 crest placed in a hand-written
	// square box was squashed by 16%. A zero width means CSS's `width: auto`.
	root := t.TempDir()
	name := writePNG(t, root, "badge.png", 96, 81)

	spec := pptxlayout.Spec{Shapes: []pptxlayout.ShapeSpec{
		{Kind: "picture", From: "badge", At: []float64{0.625, 1.45, 0, 0.958}},
	}}
	o := opts(pptxlayout.SlideMeasurement{})
	o.DeckRoot = root

	shapes, _, err := pptxlayout.Shapes(spec, deck.Slide{Number: 1, Badge: name}, o)
	if err != nil {
		t.Fatalf("Shapes: %v", err)
	}
	got := shapes[0].At
	if got.HIn != 0.958 {
		t.Errorf("HIn = %v, want the stated 0.958", got.HIn)
	}
	wantW := 0.958 * 96.0 / 81.0
	if got.WIn < wantW-0.001 || got.WIn > wantW+0.001 {
		t.Errorf("WIn = %v, want %v derived from the file's 96x81", got.WIn, wantW)
	}
}

func TestAPictureWithOnlyAWidthKeepsItsAspectRatio(t *testing.T) {
	root := t.TempDir()
	name := writePNG(t, root, "wide.png", 1200, 265)
	spec := pptxlayout.Spec{Shapes: []pptxlayout.ShapeSpec{
		{Kind: "picture", From: "badge", At: []float64{0, 0, 2.0754, 0}},
	}}
	o := opts(pptxlayout.SlideMeasurement{})
	o.DeckRoot = root

	shapes, _, err := pptxlayout.Shapes(spec, deck.Slide{Number: 1, Badge: name}, o)
	if err != nil {
		t.Fatalf("Shapes: %v", err)
	}
	wantH := 2.0754 * 265.0 / 1200.0
	if got := shapes[0].At.HIn; got < wantH-0.001 || got > wantH+0.001 {
		t.Errorf("HIn = %v, want %v", got, wantH)
	}
}

func TestAPictureWithNeitherDimensionIsAnError(t *testing.T) {
	root := t.TempDir()
	name := writePNG(t, root, "x.png", 10, 10)
	spec := pptxlayout.Spec{Shapes: []pptxlayout.ShapeSpec{
		{Kind: "picture", From: "badge", At: []float64{1, 1, 0, 0}},
	}}
	o := opts(pptxlayout.SlideMeasurement{})
	o.DeckRoot = root
	if _, _, err := pptxlayout.Shapes(spec, deck.Slide{Number: 1, Badge: name}, o); err == nil {
		t.Fatal("want an error: with no width and no height there is no size to derive")
	}
}

func TestAMeasuredPictureIsNotReFitted(t *testing.T) {
	// The browser already applied `width: auto`, so the measured rect is
	// correct and must be used as-is.
	root := t.TempDir()
	name := writePNG(t, root, "badge.png", 96, 81)
	spec := pptxlayout.Spec{Shapes: []pptxlayout.ShapeSpec{
		{Kind: "picture", From: "badge", Measure: ".badge"},
	}}
	m := measuredSlide(map[string]pptxlayout.Measurement{
		".badge": {X: 60, Y: 139, W: 109, H: 92},
	})
	o := opts(m)
	o.DeckRoot = root

	shapes, _, err := pptxlayout.Shapes(spec, deck.Slide{Number: 1, Badge: name}, o)
	if err != nil {
		t.Fatalf("Shapes: %v", err)
	}
	scale := 13.333 / 1280.0
	if got, want := shapes[0].At.WIn, 109*scale; got < want-0.001 || got > want+0.001 {
		t.Errorf("WIn = %v, want the measured %v", got, want)
	}
}

func TestAPictureFromATraversingPathIsRefused(t *testing.T) {
	spec := pptxlayout.Spec{Shapes: []pptxlayout.ShapeSpec{
		{Kind: "picture", From: "badge", At: []float64{0, 0, 1, 1}},
	}}
	o := opts(pptxlayout.SlideMeasurement{})
	o.DeckRoot = t.TempDir()
	_, _, err := pptxlayout.Shapes(spec, deck.Slide{Number: 1, Badge: "../../etc/passwd"}, o)
	if err == nil {
		t.Fatal("a deck-authored path that escapes the deck root must be refused")
	}
}

func TestAnAssetComesFromTheThemeNotTheDeck(t *testing.T) {
	themeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(themeDir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(themeDir, "assets"), "logo.png", 1020, 510)

	spec := pptxlayout.Spec{Shapes: []pptxlayout.ShapeSpec{
		{Kind: "picture", Asset: "assets/logo.png", At: []float64{0.625, 2.74, 0, 0.458}},
	}}
	o := opts(pptxlayout.SlideMeasurement{})
	o.DeckRoot = t.TempDir() // deliberately a different, empty root
	o.ThemeDir = themeDir

	shapes, _, err := pptxlayout.Shapes(spec, deck.Slide{Number: 1}, o)
	if err != nil {
		t.Fatalf("Shapes: %v", err)
	}
	// 2:1 at 0.458in tall.
	if got := shapes[0].At.WIn; got < 0.915 || got > 0.917 {
		t.Errorf("WIn = %v, want ~0.916 from the theme asset's 1020x510", got)
	}
}

// ── colour and coercion ─────────────────────────────────────────────────────

func TestColourFollowsATokenChain(t *testing.T) {
	tk := map[string]string{"brand": "var(--accent)", "accent": "#02accf", "short": "#0cf"}
	for in, want := range map[string]string{
		"accent":  "02ACCF",
		"#02accf": "02ACCF",
		"02accf":  "02ACCF",
		"short":   "00CCFF",
		"unknown": "",
		"":        "",
	} {
		if got := pptxlayout.Colour(tk, in); got != want {
			t.Errorf("Colour(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestColourStopsOnATokenCycle(t *testing.T) {
	// A theme that defines two tokens in terms of each other must not hang
	// the build.
	tk := map[string]string{"a": "b", "b": "a"}
	if got := pptxlayout.Colour(tk, "a"); got != "" {
		t.Errorf("Colour = %q, want empty rather than looping", got)
	}
}

func TestTextCoercesTheScalarsAFrontMatterCanHold(t *testing.T) {
	spec := pptxlayout.Spec{Shapes: []pptxlayout.ShapeSpec{
		{Kind: "text", From: "n", At: []float64{0, 0, 1, 1}},
	}}
	for _, tc := range []struct {
		value any
		want  string
	}{
		{42, "42"},
		{9.8, "9.8"},
		{true, "true"},
		{"plain", "plain"},
	} {
		shapes, _, err := pptxlayout.Shapes(spec,
			deck.Slide{Number: 1, Fields: map[string]any{"n": tc.value}},
			opts(pptxlayout.SlideMeasurement{}))
		if err != nil {
			t.Fatalf("Shapes(%v): %v", tc.value, err)
		}
		if len(shapes) != 1 || shapes[0].Text != tc.want {
			t.Errorf("%#v rendered as %q, want %q", tc.value, shapes[0].Text, tc.want)
		}
	}
}

func TestChromeKeysAreReadableByTheirFrontMatterNames(t *testing.T) {
	spec := pptxlayout.Spec{Shapes: []pptxlayout.ShapeSpec{
		{Kind: "text", From: "kicker", At: []float64{0, 0, 1, 1}},
		{Kind: "text", From: "phase", At: []float64{0, 1, 1, 1}},
	}}
	shapes, _, err := pptxlayout.Shapes(spec,
		deck.Slide{Number: 1, Kicker: "Benchmark", Phase: "Medição"},
		opts(pptxlayout.SlideMeasurement{}))
	if err != nil {
		t.Fatalf("Shapes: %v", err)
	}
	if len(shapes) != 2 || shapes[0].Text != "Benchmark" || shapes[1].Text != "Medição" {
		t.Errorf("chrome keys did not resolve: %+v", shapes)
	}
}

func TestNamesListsDescribedLayouts(t *testing.T) {
	got := pptxlayout.Names(map[string]pptxlayout.Spec{"table": {}, "bullets": {}, "cover": {}})
	want := []string{"bullets", "cover", "table"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Names = %v, want %v sorted", got, want)
		}
	}
}
