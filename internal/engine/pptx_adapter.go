package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine/chrome"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine/pptx"
)

// PPTXRenderer writes a PowerPoint file. With Shapes populated a slide becomes
// native and editable; without, it is image mode: one full-bleed render
// per slide, with the slide's text kept in the speaker notes.
//
// The honest framing for each, repeated in the docs: image mode is exact and
// nothing in it can be changed; native mode is editable but its geometry comes
// from the theme's PPTX layout spec rather than from the CSS, so it does not
// match the PDF pixel for pixel. Neither replaces the other, which is why the
// mode is a flag. The deck source remains the authoritative artefact in both.
type PPTXRenderer struct {
	Client Capturer
	Scale  float64
	// Notes supplies per-slide speaker notes, indexed from 1. Missing entries
	// simply produce an empty notes page.
	Notes map[int]string
	// Shapes supplies per-slide native shapes, indexed from 1, the same shape
	// as Notes. A slide with no entry keeps its full-bleed image, which is how
	// a deck can be partly editable: a layout the theme has not described in
	// PPTX terms falls back rather than failing.
	Shapes map[int][]pptx.Shape
}

// NewPPTXRenderer returns a PPTX adapter backed by headless Chrome.
func NewPPTXRenderer() *PPTXRenderer {
	return &PPTXRenderer{Client: chrome.New(), Scale: DefaultPNGScale}
}

func (r *PPTXRenderer) Format() Format { return FormatPPTX }

func (r *PPTXRenderer) Tool() string {
	for _, name := range chromeBinaries {
		if toolAvailable(name) {
			return name
		}
	}
	return chromeBinaries[0]
}

func (r *PPTXRenderer) Available() bool {
	for _, name := range chromeBinaries {
		if toolAvailable(name) {
			return true
		}
	}
	return false
}

// Render writes <OutDir>/<Slug>.pptx and returns its path.
func (r *PPTXRenderer) Render(ctx context.Context, job Job) (string, error) {
	if !r.Available() {
		return "", fmt.Errorf("no browser found on PATH (looked for %v); install one or drop pptx from -formats", chromeBinaries)
	}
	if _, err := os.Stat(job.IndexPath); err != nil {
		return "", fmt.Errorf("pptx: %w", err)
	}

	client := r.Client
	if client == nil {
		client = chrome.New()
	}
	scale := r.Scale
	if scale <= 0 {
		scale = DefaultPNGScale
	}

	target := FileURL(job.IndexPath)
	shots, err := client.CaptureAll(ctx, target, SlideSelector, scale)
	if err != nil {
		return "", fmt.Errorf("pptx: %w", err)
	}

	deck := pptx.Deck{WidthIn: job.Size.WidthIn, HeightIn: job.Size.HeightIn, Lang: job.Lang}
	for _, shot := range shots {
		deck.Slides = append(deck.Slides, pptx.Slide{
			PNG:    shot.PNG,
			Notes:  r.Notes[shot.Index],
			Shapes: r.Shapes[shot.Index],
		})
	}

	out := filepath.Join(job.OutDir, job.Slug+".pptx")
	if err := pptx.Write(out, deck); err != nil {
		return "", err
	}
	return out, nil
}
