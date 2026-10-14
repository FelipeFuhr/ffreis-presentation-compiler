// Package deck holds the content model: a deck is metadata plus an ordered
// list of slides, each naming the layout that renders it. The model is
// deliberately open — chrome fields (kicker, title, badge…) are typed because
// every layout honours them, while layout-specific keys land in Fields so a new
// layout needs a template and a contract entry, not a Go struct change.
package deck

import (
	"fmt"
	"html/template"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/seo"
)

// Aspect ratios the compiler knows how to page.
const (
	AspectWide     = "16:9"
	AspectStandard = "4:3"
)

// DefaultLang is the last resort when neither the deck nor the caller names a
// language. It has to be something — `<html lang>` and schema.org's
// inLanguage both want a value, and omitting it tells a screen reader to guess
// — but a public, consumer-agnostic compiler must not default to any one
// consumer's language. "en" is the neutral choice, and a deck that is not in
// English says so in its own deck.yaml rather than at every call site.
const DefaultLang = "en"

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

	// Lang is the deck's BCP 47 language tag. It belongs to the content, not
	// to the invocation, so it lives here; the CLI's -lang flag overrides it.
	Lang string `yaml:"lang"`

	// SEO is the discovery surface for a deck served over HTTP. Omitting the
	// block entirely is the normal case and keeps the deck noindex.
	SEO seo.Meta `yaml:"seo"`

	Slides []Slide `yaml:"-"`
}

// Language resolves the document language: an explicit override wins, then the
// deck's own declaration, then DefaultLang.
func (d *Deck) Language(override string) string {
	for _, candidate := range []string{override, d.Lang, DefaultLang} {
		if candidate != "" {
			return candidate
		}
	}
	return DefaultLang
}

// Document summarises the deck for the SEO emitter, which is a leaf package
// and so cannot be handed a *Deck.
func (d *Deck) Document(lang string) seo.Document {
	return seo.Document{
		Title:    d.Title,
		Subtitle: d.Subtitle,
		Lang:     d.Language(lang),
		Slides:   len(d.Slides),
	}
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
