package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/check"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine/chrome"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/render"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/theme"
)

type checkOptions struct {
	dir         string
	themeRoot   string
	themeName   string
	minFont     float64
	tolerance   float64
	overlapArea float64
	pdfPath     string
}

func parseCheckOptions(args []string, stderr io.Writer) (*checkOptions, error) {
	var o checkOptions
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.dir, "dir", "dist", "directory holding a rendered deck (index.html)")
	fs.StringVar(&o.themeRoot, "theme-root", "", "themes root, so expected fonts can be checked (optional)")
	fs.StringVar(&o.themeName, "theme", "", "theme name, used with -theme-root")
	fs.Float64Var(&o.minFont, "min-font-px", check.DefaultMinFontPx, "smallest tolerated rendered type, in CSS px")
	fs.Float64Var(&o.tolerance, "overflow-tolerance-px", check.DefaultTolerancePx,
		"overflow below this is treated as fractional-pixel rounding, not a defect")
	fs.Float64Var(&o.overlapArea, "overlap-min-area-px", check.DefaultOverlapMinAreaPx,
		"intersecting area below this is treated as a touching edge, not a collision (CSS px^2)")
	fs.StringVar(&o.pdfPath, "pdf", "", "path to a rendered PDF; its page count is compared against the slide count (optional)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return &o, nil
}

// runCheck measures a rendered deck and fails when anything is wrong. It
// deliberately reads the built output rather than re-rendering: what ships is
// what gets checked.
func runCheck(args []string, stdout, stderr io.Writer) int {
	opts, err := parseCheckOptions(args, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "check: %v\n", err)
		return exitUsage
	}

	index, err := filepath.Abs(filepath.Join(opts.dir, render.IndexFile))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "check: %v\n", err)
		return exitError
	}

	runOpts := check.Options{MinFontPx: opts.minFont, TolerancePx: opts.tolerance, OverlapMinAreaPx: opts.overlapArea}
	if opts.themeRoot != "" && opts.themeName != "" {
		th, err := theme.Load(opts.themeRoot, opts.themeName)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "check: %v\n", err)
			return exitError
		}
		runOpts.Fonts = th.Fonts
	}

	// pdf-pages is not a DOM measurement, so it is resolved before the
	// browser launches — like the theme load above, a bad -pdf path fails
	// fast rather than paying for a Chrome round trip first.
	var pdfFinding *check.Finding
	if opts.pdfPath != "" {
		pdfFinding, err = comparePDFPages(index, opts.pdfPath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "check: %v\n", err)
			return exitError
		}
	}

	target := engine.FileURL(index)

	findings, err := check.Run(context.Background(), chrome.New(), target, runOpts)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "check: %v\n", err)
		return exitError
	}
	findings = mergeFinding(findings, pdfFinding)

	_, _ = fmt.Fprint(stdout, check.Report(findings))
	if len(findings) > 0 {
		return exitError
	}
	return exitOK
}

// comparePDFPages counts the [data-slide] sections in the already-rendered
// index.html and compares that against pdfPath's page count. Reading the
// slide count from disk, rather than from the browser, is what lets this run
// without Chrome.
func comparePDFPages(index, pdfPath string) (*check.Finding, error) {
	html, err := os.ReadFile(index) //nolint:gosec // index is derived from the operator-supplied -dir flag
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", index, err)
	}
	return check.ComparePDFPages(pdfPath, check.CountSlideSections(html))
}

// mergeFinding appends extra to findings, keeping them sorted, when extra is
// not nil. It is its own function, pulled out of runCheck, so the merge that
// runs after the one call in this file needing a real browser can be tested
// on its own without one.
func mergeFinding(findings []check.Finding, extra *check.Finding) []check.Finding {
	if extra == nil {
		return findings
	}
	findings = append(findings, *extra)
	check.SortFindings(findings)
	return findings
}
