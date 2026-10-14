package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/golden"
)

// defaultGoldenBaseline is the committed baseline the shipped example deck is
// compared against; see examples/golden/.
const defaultGoldenBaseline = "examples/golden"

// defaultGoldenDiffDir is where a mismatch's highlight artifact lands, so a
// human has somewhere to look without being told a bespoke path every time.
const defaultGoldenDiffDir = ".bin/golden"

type goldenOptions struct {
	dir       string
	baseline  string
	diffDir   string
	tolerance float64
}

func parseGoldenOptions(args []string, stderr io.Writer) (*goldenOptions, error) {
	var o goldenOptions
	fs := flag.NewFlagSet("golden", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.dir, "dir", "", "directory holding freshly rendered slide PNGs (required)")
	fs.StringVar(&o.baseline, "baseline", defaultGoldenBaseline, "directory holding the approved baseline PNGs")
	fs.StringVar(&o.diffDir, "diff-dir", defaultGoldenDiffDir, "directory to write a highlight artifact for any mismatch")
	fs.Float64Var(&o.tolerance, "tolerance", golden.DefaultDiffRatioTolerance,
		"fraction of a slide's pixels allowed to differ before it counts as a regression")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if o.dir == "" {
		return nil, fmt.Errorf("-dir is required")
	}
	return &o, nil
}

// runGolden compares freshly rendered slide PNGs against the approved
// baseline under opts.baseline. It never writes to the baseline — re-approving
// it is `make golden-update`'s job, which does the copy itself in the
// Makefile rather than through a flag here, so a typo can never turn a
// compare into a silent re-approval.
func runGolden(args []string, stdout, stderr io.Writer) int {
	opts, err := parseGoldenOptions(args, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "golden: %v\n", err)
		return exitUsage
	}

	results, err := golden.CompareDir(opts.baseline, opts.dir, opts.diffDir, opts.tolerance)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "golden: %v\n", err)
		return exitError
	}

	_, _ = fmt.Fprint(stdout, golden.Report(results, opts.tolerance))
	if golden.HasMismatch(results, opts.tolerance) {
		return exitError
	}
	return exitOK
}
