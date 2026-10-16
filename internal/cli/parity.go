package cli

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/parity"
)

type parityOptions struct {
	deckRoot  string
	dir       string
	lang      string
	tolerance float64
}

func parseParityOptions(args []string, stderr io.Writer) (*parityOptions, error) {
	var o parityOptions
	fs := flag.NewFlagSet("parity", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.deckRoot, "deck-root", "", "directory holding deck.yaml and slides/ (required)")
	fs.StringVar(&o.dir, "dir", "", "directory holding freshly rendered slide PNG files (required)")
	fs.StringVar(&o.lang, "lang", "",
		"tesseract language code (default: derived from the deck's own lang:, falling back to "+parity.DefaultOCRLang+")")
	fs.Float64Var(&o.tolerance, "tolerance", parity.DefaultMissingWordTolerance,
		"fraction of a slide's own significant words allowed to be missing from its OCR text")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if o.deckRoot == "" {
		return nil, fmt.Errorf("-deck-root is required")
	}
	if o.dir == "" {
		return nil, fmt.Errorf("-dir is required")
	}
	return &o, nil
}

// runParity OCRs a deck's own rendered slide PNG files and compares the result
// against the deck's authored source, so a dropped section or annotation is
// a build-time finding rather than something only a line-by-line read
// catches. Advisory by convention the same way `review` is: it needs an
// external tool (`doctor` reports it) and is fuzzy by nature — see
// internal/parity's package doc for what it can and cannot catch.
func runParity(args []string, stdout, stderr io.Writer) int {
	opts, err := parseParityOptions(args, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "parity: %v\n", err)
		return exitUsage
	}
	if !parity.Available() {
		_, _ = fmt.Fprintf(stderr, "parity: needs %q on PATH\n", parity.Tool)
		return exitError
	}

	d, err := deck.Load(opts.deckRoot)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "parity: %v\n", err)
		return exitError
	}

	lang := opts.lang
	if lang == "" {
		lang = parity.OCRLang(d.Language(""))
	}

	results, err := parity.CompareDeck(context.Background(), parity.ExecRunner, d, opts.dir, lang)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "parity: %v\n", err)
		return exitError
	}

	_, _ = fmt.Fprint(stdout, parity.Report(results, opts.tolerance))
	if parity.HasMismatch(results, opts.tolerance) {
		return exitError
	}
	return exitOK
}
