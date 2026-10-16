package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine/chrome"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine/pptx"
)

// PPTXRenderer writes a PowerPoint file in image mode: one full-bleed render
// per slide, with the slide's text kept in the speaker notes.
//
// The honest framing, repeated in the docs: fidelity is exact and nothing is
// editable. The deck source stays the editable artefact; this is for handing a
// file to someone who expects .pptx.
type PPTXRenderer struct {
	Client Capturer
	Scale  float64
	// Notes supplies per-slide speaker notes, indexed from 1. Missing entries
	// simply produce an empty notes page.
	Notes map[int]string
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
		deck.Slides = append(deck.Slides, pptx.Slide{PNG: shot.PNG, Notes: r.Notes[shot.Index]})
	}

	out := filepath.Join(job.OutDir, job.Slug+".pptx")
	if err := pptx.Write(out, deck); err != nil {
		return "", err
	}
	return out, nil
}
