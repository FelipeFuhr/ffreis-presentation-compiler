// Package deck holds the content model: a deck is metadata plus an ordered
// list of slides, each naming the layout that renders it. The model is
// deliberately open — chrome fields (kicker, title, badge…) are typed because
// every layout honours them, while layout-specific keys land in Fields so a new
// layout needs a template and a contract entry, not a Go struct change.
package deck

import (
	"fmt"
	"html/template"
)

// Aspect ratios the compiler knows how to page.
const (
	AspectWide     = "16:9"
	AspectStandard = "4:3"
)

// Size is a slide's physical size in inches. Inches (not pixels) because the
// same number has to satisfy CSS `@page { size: … }` and the PDF print path.
type Size struct {
	WidthIn  float64
	HeightIn float64
}

// Deck is one presentation.
type Deck struct {
	Title    string `yaml:"title"`
	Subtitle string `yaml:"subtitle"`
	Client   string `yaml:"client"`
	Theme    string `yaml:"theme"`
	Aspect   string `yaml:"aspect"`
	Footer   string `yaml:"footer"`

	Slides []Slide `yaml:"-"`
}

// Slide is one rendered page.
type Slide struct {
	// Chrome — honoured by every layout.
	Layout string `yaml:"layout"`
	Kicker string `yaml:"kicker"`
	Title  string `yaml:"title"`
	Phase  string `yaml:"phase"`
	Badge  string `yaml:"badge"`
	Notes  string `yaml:"notes"`
	Skip   bool   `yaml:"skip"`

	// Fields carries every front-matter key that is not chrome, so layouts can
	// declare their own vocabulary (rows, columns, items, media…).
	Fields map[string]any `yaml:"-"`

	// Body is the Markdown after the front-matter, already rendered.
	Body template.HTML `yaml:"-"`

	// Source is the file this slide was read from, used in error messages.
	Source string `yaml:"-"`

	// Number is the 1-based position in the built deck, assigned after skipped
	// slides are dropped.
	Number int `yaml:"-"`
}

// Size resolves the deck's aspect to physical dimensions, defaulting to 16:9.
func (d *Deck) Size() (Size, error) {
	switch d.Aspect {
	case "", AspectWide:
		return Size{WidthIn: 13.333, HeightIn: 7.5}, nil
	case AspectStandard:
		return Size{WidthIn: 10, HeightIn: 7.5}, nil
	default:
		return Size{}, fmt.Errorf("unknown aspect %q: want %q or %q", d.Aspect, AspectWide, AspectStandard)
	}
}

// Field returns a layout-specific field as a string, empty when absent.
func (s *Slide) Field(name string) string {
	v, ok := s.Fields[name]
	if !ok {
		return ""
	}
	str, ok := v.(string)
	if !ok {
		return ""
	}
	return str
}
