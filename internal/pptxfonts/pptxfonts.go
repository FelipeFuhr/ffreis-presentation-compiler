// Package pptxfonts decides which font bytes a .pptx should carry.
//
// It is the join between three things that should not know about each other:
// fontsource finds files on disk, engine/pptx writes OOXML containers, and
// neither should hold the policy about when embedding is worth doing. That
// policy is short but it is a judgement, and it was previously inline in the
// build command alongside flag parsing and render orchestration, where it
// could not be tested without running a build.
package pptxfonts

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine/pptx"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/fontsource"
)

// Load resolves typeface to font files and returns what a .pptx should embed,
// along with notes a human should read.
//
// Notes rather than errors, except for a font that cannot be read at all: a
// deck whose typeface could not be embedded still renders, substituting, and
// failing the build over it would make a missing font file fatal to a format
// the author may not even have asked for. What must not happen is silence.
func Load(ctx context.Context, typeface string, opt fontsource.Options) ([]pptx.EmbeddedFont, []string, error) {
	name := strings.TrimSpace(typeface)
	if name == "" {
		return nil, nil, nil
	}

	// A face Office ships cannot substitute, so embedding it buys nothing and
	// costs a megabyte or more in a file that gets emailed.
	if pptx.AssumedPresent(name) {
		return nil, []string{fmt.Sprintf(
			"%s ships with Office, so it is named rather than embedded", name)}, nil
	}

	faces, notes, err := fontsource.Resolve(ctx, name, opt)
	if err != nil {
		return nil, nil, err
	}
	if len(faces) == 0 {
		return nil, append(notes, fmt.Sprintf(
			"%s could not be embedded: no .ttf or .otf for it was found, so a reader without "+
				"it installed will substitute", name)), nil
	}

	font := pptx.EmbeddedFont{Typeface: name}
	var loaded []string
	for _, f := range faces {
		// Paths come from a theme directory the operator named or from
		// fontconfig, neither of which is deck content.
		body, err := os.ReadFile(f.Path)
		if err != nil {
			return nil, nil, fmt.Errorf("pptxfonts: read %s: %w", f.Path, err)
		}
		assign(&font, f.Variant, body)
		loaded = append(loaded, fmt.Sprintf("%s from %s", f.Variant, f.Source))
	}
	return []pptx.EmbeddedFont{font}, append(notes, fmt.Sprintf(
		"embedding %s so it cannot substitute: %s", name, strings.Join(loaded, "; "))), nil
}

// assign puts one variant's bytes in the field the writer reads.
func assign(font *pptx.EmbeddedFont, v fontsource.Variant, body []byte) {
	switch v {
	case fontsource.Regular:
		font.Regular = body
	case fontsource.Bold:
		font.Bold = body
	case fontsource.Italic:
		font.Italic = body
	case fontsource.BoldItalic:
		font.BoldItalic = body
	}
}
