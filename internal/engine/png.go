package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine/chrome"
)

// SlidesDir is where per-slide rasters are written, relative to the output dir.
const SlidesDir = "slides"

// SlideSelector matches one rendered slide. The compiler's document shell puts
// data-slide on every section, so this is a contract between render and here.
const SlideSelector = "[data-slide]"

// DefaultPNGScale renders at twice CSS resolution: enough that a slide image
// dropped into PPTX or a review tool still looks sharp on a high-DPI screen.
const DefaultPNGScale = 2.0

// Capturer screenshots elements in a page. It is a port so the adapter is
// testable without a browser.
type Capturer interface {
	CaptureAll(ctx context.Context, pageURL, selector string, scale float64) ([]chrome.Shot, error)
}

// PNGRenderer writes one image per slide.
type PNGRenderer struct {
	Client Capturer
	Scale  float64
}

// NewPNGRenderer returns a PNG adapter backed by headless Chrome.
func NewPNGRenderer() *PNGRenderer { return &PNGRenderer{Client: chrome.New(), Scale: DefaultPNGScale} }

func (r *PNGRenderer) Format() Format { return FormatPNG }

func (r *PNGRenderer) Tool() string {
	for _, name := range chromeBinaries {
		if toolAvailable(name) {
			return name
		}
	}
	return chromeBinaries[0]
}

func (r *PNGRenderer) Available() bool {
	for _, name := range chromeBinaries {
		if toolAvailable(name) {
			return true
		}
	}
	return false
}

// Render writes <OutDir>/slides/NN.png and returns the directory.
func (r *PNGRenderer) Render(ctx context.Context, job Job) (string, error) {
	if !r.Available() {
		return "", fmt.Errorf("no browser found on PATH (looked for %v); install one or drop png from -formats", chromeBinaries)
	}
	if _, err := os.Stat(job.IndexPath); err != nil {
		return "", fmt.Errorf("png: %w", err)
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
		return "", fmt.Errorf("png: %w", err)
	}

	dir := filepath.Join(job.OutDir, SlidesDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("png: %w", err)
	}
	for _, shot := range shots {
		path := filepath.Join(dir, fmt.Sprintf("%02d.png", shot.Index))
		if err := os.WriteFile(path, shot.PNG, 0o600); err != nil {
			return "", fmt.Errorf("png: write %s: %w", path, err)
		}
	}
	return dir, nil
}
