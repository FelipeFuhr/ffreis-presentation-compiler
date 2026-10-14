package gen

import (
	"context"
	"fmt"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
)

// SpecKeys are the front-matter keys the compiler interprets as a generator
// invocation. Both produce an image; the two names exist so a slide reads as
// what it is.
var SpecKeys = []string{"chart", "diagram"}

// MediaKey is the field a resolved generator writes its output path to, so
// layouts keep reading `.Fields.media` whether the image was authored or
// generated.
const MediaKey = "media"

// Options carries the roots a generation pass needs.
type Options struct {
	DeckRoot string
	OutDir   string
	Tokens   map[string]string
}

// Apply resolves every generator declared in the deck, writing each result to
// the slide's `media` field. Slides without a generator are untouched.
func (r Registry) Apply(ctx context.Context, run Runner, d *deck.Deck, opts Options) error {
	for i := range d.Slides {
		s := &d.Slides[i]
		spec, key, err := SpecOf(s)
		if err != nil {
			return fmt.Errorf("slide %d (%s): %w", s.Number, s.Source, err)
		}
		if spec == nil {
			continue
		}
		if _, taken := s.Fields[MediaKey]; taken {
			return fmt.Errorf("slide %d (%s): sets both %q and %q — %[4]q is what %[3]q produces",
				s.Number, s.Source, key, MediaKey)
		}

		req := Request{Spec: *spec, DeckRoot: opts.DeckRoot, OutDir: opts.OutDir, Tokens: opts.Tokens}
		path, err := r.Resolve(ctx, run, req)
		if err != nil {
			return fmt.Errorf("slide %d (%s): %w", s.Number, s.Source, err)
		}
		s.Fields[MediaKey] = path
	}
	return nil
}

// SpecOf extracts a generator spec from a slide's fields, returning the key
// it came from for error messages. Exported so internal/contract can
// validate a chart/diagram block's spec/data paths with the exact same
// parsing Apply uses, instead of a second copy that could drift from it.
func SpecOf(s *deck.Slide) (*Spec, string, error) {
	for _, key := range SpecKeys {
		raw, ok := s.Fields[key]
		if !ok {
			continue
		}
		m, ok := raw.(map[string]any)
		if !ok {
			return nil, key, fmt.Errorf("%q must be a mapping with generator/spec keys", key)
		}
		spec := &Spec{
			Generator: stringField(m, "generator"),
			Spec:      stringField(m, "spec"),
			Data:      stringField(m, "data"),
			Args:      stringSlice(m["args"]),
		}
		if spec.Generator == "" {
			return nil, key, fmt.Errorf("%q does not name a generator", key)
		}
		return spec, key, nil
	}
	return nil, "", nil
}

func stringField(m map[string]any, key string) string {
	v, ok := m[key].(string)
	if !ok {
		return ""
	}
	return v
}

func stringSlice(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
