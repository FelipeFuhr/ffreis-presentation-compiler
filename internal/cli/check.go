package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/check"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/render"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/theme"
)

type checkOptions struct {
	dir       string
	themeRoot string
	themeName string
	minFont   float64
	tolerance float64
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

	runOpts := check.Options{MinFontPx: opts.minFont, TolerancePx: opts.tolerance}
	if opts.themeRoot != "" && opts.themeName != "" {
		th, err := theme.Load(opts.themeRoot, opts.themeName)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "check: %v\n", err)
			return exitError
		}
		runOpts.Fonts = th.Fonts
	}

	findings, err := check.Run(context.Background(), check.NewChromeBrowser(), "file://"+index, runOpts)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "check: %v\n", err)
		return exitError
	}

	_, _ = fmt.Fprint(stdout, check.Report(findings))
	if len(findings) > 0 {
		return exitError
	}
	return exitOK
}
