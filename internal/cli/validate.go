package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/contract"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/theme"
)

type validateOptions struct {
	deckRoot  string
	themeRoot string
}

func parseValidateOptions(args []string, stderr io.Writer) (*validateOptions, error) {
	var o validateOptions
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.deckRoot, "deck-root", "", "deck directory (deck.yaml + slides/)")
	fs.StringVar(&o.themeRoot, "theme-root", "", "directory holding themes, one subdirectory per theme")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if o.deckRoot == "" {
		return nil, fmt.Errorf("-deck-root is required")
	}
	if o.themeRoot == "" {
		return nil, fmt.Errorf("-theme-root is required")
	}
	return &o, nil
}

// runValidate checks a deck against its theme's field contract before
// anything renders: today deck.Load only fails on a missing layout key, and a
// bad field beyond that would otherwise reach a template and produce either
// a blank region or a confusing template error. It reports every problem it
// finds, not just the first.
func runValidate(args []string, stdout, stderr io.Writer) int {
	opts, err := parseValidateOptions(args, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "validate: %v\n", err)
		return exitUsage
	}

	d, err := deck.Load(opts.deckRoot)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "validate: %v\n", err)
		return exitError
	}
	th, err := theme.Load(opts.themeRoot, d.Theme)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "validate: %v\n", err)
		return exitError
	}

	findings := contract.Validate(d, th, opts.deckRoot)
	_, _ = fmt.Fprint(stdout, contract.Report(findings))
	if contract.Fatal(findings) {
		return exitError
	}
	return exitOK
}
