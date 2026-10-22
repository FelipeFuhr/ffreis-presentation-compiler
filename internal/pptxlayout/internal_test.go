package pptxlayout

import "testing"

func TestCSSColourConversion(t *testing.T) {
	for _, tc := range []struct{ in, want, why string }{
		{"rgb(2, 172, 207)", "02ACCF", "plain rgb"},
		{"rgb(255,255,255)", "FFFFFF", "no spaces"},
		{"rgba(52, 59, 67, 1)", "343B43", "fully opaque rgba is representable"},
		{"rgba(2, 172, 207, 0.1)", "", "a PPTX solid fill has no alpha, so translucent is refused"},
		{"rgb(2 172 207 / 1)", "02ACCF", "space-separated with an opaque slash alpha"},
		{"color(display-p3 1 0 0)", "", "a colour space OOXML cannot express"},
		{"", "", "empty"},
		// Clamping is reachable because the pattern accepts any number of
		// digits; a negative channel is not, since a computed style is always
		// already clamped to 0-255, which is why the pattern takes no sign.
		{"rgb(300, 5, 207)", "FF05CF", "an over-range channel clamps rather than wrapping to 2C"},
	} {
		if got := cssColour(tc.in); got != tc.want {
			t.Errorf("cssColour(%q) = %q, want %q (%s)", tc.in, got, tc.want, tc.why)
		}
	}
}

func TestFirstFamilyTakesWhatTheBrowserPreferred(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`Roboto, "Helvetica Neue", sans-serif`, "Roboto"},
		{`"Fira Sans", sans-serif`, "Fira Sans"},
		{`  Georgia  `, "Georgia"},
		{"", ""},
	} {
		if got := firstFamily(tc.in); got != tc.want {
			t.Errorf("firstFamily(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCSSAlignResolvesLogicalValues(t *testing.T) {
	for in, want := range map[string]string{
		"center": "ctr", "right": "r", "end": "r", "left": "l", "start": "l", "justify": "",
	} {
		if got := cssAlign(in); got != want {
			t.Errorf("cssAlign(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMeasuredStyleConvertsBrowserUnits(t *testing.T) {
	got := measuredStyle(Measurement{
		FontSizePx:      33,
		LineHeightPx:    39.6,
		LetterSpacingPx: -0.33,
		Weight:          700,
		Color:           "rgb(31, 45, 57)",
		Align:           "start",
		Family:          `Roboto, sans-serif`,
		Transform:       "uppercase",
	})

	// 96 px per inch, 72 pt per inch: 33px is 24.75pt, which is the theme's
	// --size-title (2.0625rem) exactly.
	if got.Size < 24.74 || got.Size > 24.76 {
		t.Errorf("Size = %v pt, want 24.75", got.Size)
	}
	if !got.Bold {
		t.Error("weight 700 should be bold")
	}
	if !got.Caps {
		t.Error("text-transform: uppercase should set Caps, not upper-case the source text")
	}
	if got.Color != "#1F2D39" {
		t.Errorf("Color = %q, want #1F2D39", got.Color)
	}
	if got.Align != "l" {
		t.Errorf("Align = %q, want l", got.Align)
	}
	if got.Font != "Roboto" {
		t.Errorf("Font = %q, want Roboto", got.Font)
	}
	if got.Line < 119 || got.Line > 121 {
		t.Errorf("Line = %v%%, want ~120", got.Line)
	}
	if got.Spacing > -0.24 || got.Spacing < -0.26 {
		t.Errorf("Spacing = %v pt, want ~-0.2475", got.Spacing)
	}
}

func TestMeasuredStyleBoldThreshold(t *testing.T) {
	// 600 counts: a theme using it means bold, and PPTX has no weight axis to
	// express the difference between 600 and 700.
	for weight, bold := range map[int]bool{400: false, 500: false, 600: true, 700: true} {
		if got := measuredStyle(Measurement{Weight: weight}).Bold; got != bold {
			t.Errorf("weight %d: Bold = %v, want %v", weight, got, bold)
		}
	}
}

func TestMeasuredStyleRefusesATranslucentColour(t *testing.T) {
	if got := measuredStyle(Measurement{Color: "rgba(2, 172, 207, 0.2)"}).Color; got != "" {
		t.Errorf("Color = %q, want empty so the shape renders unfilled rather than guessing", got)
	}
}

func TestOverrideLetsTheSpecWinAndOrsTheBooleans(t *testing.T) {
	measured := Style{Size: 24.75, Color: "#1F2D39", Align: "l", Font: "Roboto", Line: 120, Bold: true}
	spec := Style{Size: 18, Color: "accent", Italic: true}

	got := override(spec, measured)

	if got.Size != 18 || got.Color != "accent" {
		t.Errorf("the spec's explicit values should win: %+v", got)
	}
	if got.Align != "l" || got.Font != "Roboto" || got.Line != 120 {
		t.Errorf("unstated values should keep the measurement: %+v", got)
	}
	// A spec cannot say "not bold" by omitting bold, so the two are ORed.
	if !got.Bold || !got.Italic {
		t.Errorf("Bold and Italic should be the union: %+v", got)
	}
}

func TestScaleIsDerivedFromTheMeasuredFrame(t *testing.T) {
	m := SlideMeasurement{FrameW: 1280}
	if got := m.scale(13.333); got < 0.010 || got > 0.011 {
		t.Errorf("scale = %v, want ~0.010417 (13.333in / 1280px)", got)
	}

	// Any render scale measures correctly, because the frame is measured too
	// rather than assumed to be 96px per inch.
	double := SlideMeasurement{FrameW: 2560}
	if a, b := m.scale(13.333)*1280, double.scale(13.333)*2560; a != b {
		t.Errorf("a 2x render should map to the same inches: %v vs %v", a, b)
	}

	if got := (SlideMeasurement{}).scale(13.333); got != 0 {
		t.Errorf("an unmeasured slide has no scale, got %v", got)
	}
}

func TestRectConvertsToInches(t *testing.T) {
	m := SlideMeasurement{FrameW: 1280}
	e := Measurement{X: 60, Y: 96, W: 1160, H: 48}
	got := e.rect(m.scale(13.333))
	if got.XIn < 0.624 || got.XIn > 0.626 {
		t.Errorf("XIn = %v, want ~0.625 (the theme's --margin)", got.XIn)
	}
	if got.YIn < 0.999 || got.YIn > 1.001 {
		t.Errorf("YIn = %v, want ~1.0", got.YIn)
	}
}
