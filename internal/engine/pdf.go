package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// chromeBinaries are tried in order; the first one on PATH wins. Matching the
// list ffreis-resume's pdf.sh settled on, so CI images that work for one work
// for the other.
var chromeBinaries = []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"}

// PDFRenderer prints the rendered HTML to a paginated PDF with headless Chrome.
//
// Page size is asserted twice — the document shell emits `@page { size: … }`
// and this adapter is free to pass explicit dimensions — because a theme that
// overrode the shell's CSS would otherwise silently change every page size.
type PDFRenderer struct {
	Runner Runner
}

// NewPDFRenderer returns a PDF adapter using the production Runner.
func NewPDFRenderer() *PDFRenderer { return &PDFRenderer{Runner: ExecRunner} }

func (r *PDFRenderer) Format() Format { return FormatPDF }

// Tool reports the browser binary this adapter would use, or the preferred name
// when none is installed, so `doctor` can name what to install.
func (r *PDFRenderer) Tool() string {
	for _, name := range chromeBinaries {
		if toolAvailable(name) {
			return name
		}
	}
	return chromeBinaries[0]
}

func (r *PDFRenderer) Available() bool {
	for _, name := range chromeBinaries {
		if toolAvailable(name) {
			return true
		}
	}
	return false
}

// Render prints job.IndexPath to <OutDir>/<Slug>.pdf and returns the path.
func (r *PDFRenderer) Render(ctx context.Context, job Job) (string, error) {
	if !r.Available() {
		return "", fmt.Errorf("no browser found on PATH (looked for %v); install one or drop pdf from -formats", chromeBinaries)
	}
	if _, err := os.Stat(job.IndexPath); err != nil {
		return "", fmt.Errorf("pdf: %w", err)
	}

	out := filepath.Join(job.OutDir, job.Slug+".pdf")
	run := r.Runner
	if run == nil {
		run = ExecRunner
	}

	target, err := FileURL(job.IndexPath)
	if err != nil {
		return "", fmt.Errorf("pdf: %w", err)
	}

	// --no-pdf-header-footer drops Chrome's default date/URL furniture;
	// --no-sandbox and --disable-dev-shm-usage are what make this work inside
	// CI containers and act, where user namespaces and /dev/shm are limited.
	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--no-pdf-header-footer",
		"--print-to-pdf=" + out,
		target,
	}
	if err := run(ctx, r.Tool(), args...); err != nil {
		return "", fmt.Errorf("pdf: %w", err)
	}
	if _, err := os.Stat(out); err != nil {
		return "", fmt.Errorf("pdf: browser exited cleanly but wrote no file at %s", out)
	}
	return out, nil
}
