package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	osexec "os/exec"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/pptxcheck"
)

type pptxCheckOptions struct {
	pptx      string
	deckRoot  string
	overlap   float64
	minType   float64
	edge      float64
	tolerance float64
}

func parsePPTXCheckOptions(args []string, stderr io.Writer) (*pptxCheckOptions, error) {
	var o pptxCheckOptions
	fs := flag.NewFlagSet("pptx-check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.pptx, "pptx", "", "the .pptx to render and measure (required)")
	fs.StringVar(&o.deckRoot, "deck-root", "",
		"deck root, so the words each slide declares can be checked against the ones that rendered (optional)")
	fs.Float64Var(&o.overlap, "overlap-tolerance-pt", pptxcheck.DefaultOverlapTolerancePt,
		"vertical interpenetration below this is ascent/descent, not a collision")
	fs.Float64Var(&o.minType, "min-type-pt", pptxcheck.DefaultMinTypePt,
		"smallest tolerated rendered line height, in points")
	fs.Float64Var(&o.edge, "edge-tolerance-pt", pptxcheck.DefaultEdgeTolerancePt,
		"how far a word may sit past the slide edge before it counts as off-slide")
	fs.Float64Var(&o.tolerance, "missing-word-tolerance", pptxcheck.DefaultMissingWordTolerance,
		"fraction of a slide's declared words that may be absent from the render")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if o.pptx == "" {
		return nil, fmt.Errorf("-pptx is required")
	}
	return &o, nil
}

// runPPTXCheck measures a rendered .pptx.
//
// Separate from `check` rather than a flag on it, because they measure
// different artifacts through different tools: `check` drives Chrome over the
// DOM, this drives a document reader over the exported file. Folding them
// together would mean one command that needs both a browser and LibreOffice
// to say anything at all.
func runPPTXCheck(args []string, stdout, stderr io.Writer) int {
	opts, err := parsePPTXCheckOptions(args, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "pptx-check: %v\n", err)
		return exitUsage
	}
	if _, err := os.Stat(opts.pptx); err != nil {
		_, _ = fmt.Fprintf(stderr, "pptx-check: %v\n", err)
		return exitError
	}

	// Resolved before the reader is launched, so a bad -deck-root fails fast
	// rather than after a conversion -- the same ordering `check` uses for its
	// theme load and its -pdf path.
	var d *deck.Deck
	if opts.deckRoot != "" {
		d, err = deck.Load(opts.deckRoot)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "pptx-check: %v\n", err)
			return exitError
		}
	}

	// Built from DefaultOptions rather than a bare literal so a threshold
	// added to the package later arrives tuned here instead of zeroed.
	tuned := pptxcheck.DefaultOptions()
	tuned.OverlapTolerancePt = opts.overlap
	tuned.MinTypePt = opts.minType
	tuned.EdgeTolerancePt = opts.edge
	tuned.MissingWordTolerance = opts.tolerance

	result, err := pptxcheck.Run(context.Background(), pptxRunner, opts.pptx, d, tuned)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "pptx-check: %v\n", err)
		return exitError
	}

	_, _ = fmt.Fprint(stdout, pptxcheck.Report(result))
	if len(result.Findings) > 0 {
		return exitError
	}
	return exitOK
}

// pptxRunner is a variable for the same reason engine.LookPath is: it is this
// command's only boundary onto external tools, and a test must be able to
// drive the command without LibreOffice deciding whether it passes.
var pptxRunner pptxcheck.Runner = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	return osexec.CommandContext(ctx, name, args...).Output() //nolint:gosec // the two readers this command is built on
}
