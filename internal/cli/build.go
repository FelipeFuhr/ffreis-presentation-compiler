package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/check"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/gen"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/logx"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/render"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/theme"
)

// buildOptions is the parsed `build` flag set. The compiler takes every path as
// a flag and hardcodes no consumer, which is what keeps this repo publishable.
type buildOptions struct {
	deckRoot  string
	themeRoot string
	out       string
	formats   string
	slug      string
	lang      string
	verbose   bool
}

func parseBuildOptions(args []string, stderr io.Writer) (*buildOptions, error) {
	var o buildOptions
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.deckRoot, "deck-root", "", "deck directory (deck.yaml + slides/)")
	fs.StringVar(&o.themeRoot, "theme-root", "", "directory holding themes, one subdirectory per theme")
	fs.StringVar(&o.out, "out", "dist", "output directory")
	fs.StringVar(&o.formats, "formats", "html,pdf", "comma-separated outputs: html, pdf, png")
	fs.StringVar(&o.slug, "slug", "", "artifact basename (default: the deck directory name)")
	fs.StringVar(&o.lang, "lang", "pt-BR", "document language")
	fs.BoolVar(&o.verbose, "v", false, "verbose logging")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if o.deckRoot == "" {
		return nil, fmt.Errorf("-deck-root is required")
	}
	if o.themeRoot == "" {
		return nil, fmt.Errorf("-theme-root is required")
	}
	if o.slug == "" {
		o.slug = filepath.Base(filepath.Clean(o.deckRoot))
	}
	return &o, nil
}

func runBuild(args []string, stdout, stderr io.Writer) int {
	opts, err := parseBuildOptions(args, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "build: %v\n", err)
		return exitUsage
	}
	logger := logx.New(stderr, opts.verbose)

	formats, err := engine.ParseFormats(opts.formats)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "build: %v\n", err)
		return exitUsage
	}

	d, err := deck.Load(opts.deckRoot)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "build: %v\n", err)
		return exitError
	}
	th, err := theme.Load(opts.themeRoot, d.Theme)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "build: %v\n", err)
		return exitError
	}
	logger.Debug("loaded", "slides", len(d.Slides), "theme", th.Name, "layouts", th.LayoutNames())

	// Generators run before rendering: each writes an image and sets the
	// slide's media field, so layouts cannot tell a generated chart from an
	// authored one.
	genOpts := gen.Options{DeckRoot: opts.deckRoot, OutDir: opts.out, Tokens: th.Tokens()}
	if err := gen.DefaultRegistry().Apply(context.Background(), engine.ExecRunner, d, genOpts); err != nil {
		_, _ = fmt.Fprintf(stderr, "build: %v\n", err)
		return exitError
	}

	renderOpts := render.Options{DeckRoot: opts.deckRoot, OutDir: opts.out, Lang: opts.lang}
	if err := render.Render(d, th, renderOpts); err != nil {
		_, _ = fmt.Fprintf(stderr, "build: %v\n", err)
		return exitError
	}
	index, err := filepath.Abs(filepath.Join(opts.out, render.IndexFile))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "build: %v\n", err)
		return exitError
	}
	_, _ = fmt.Fprintln(stdout, index)

	size, err := d.Size()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "build: %v\n", err)
		return exitError
	}
	job := engine.Job{IndexPath: index, OutDir: opts.out, Slug: opts.slug, Size: size}
	paths, err := renderFormats(context.Background(), formats, job, speakerNotes(d), stdout)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "build: %v\n", err)
		return exitError
	}

	// A silently dropped or doubled PDF page is invisible until someone
	// presents, so the common path checks for it without anyone needing to
	// remember a flag — whenever this build actually produced a PDF.
	finding, err := checkPDFPageCount(paths, len(d.Slides))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "build: %v\n", err)
		return exitError
	}
	if finding != nil {
		_, _ = fmt.Fprintf(stderr, "build: %s\n", finding.Detail)
		return exitError
	}
	return exitOK
}

// checkPDFPageCount compares a produced PDF's page count against slideCount.
// It is a no-op, returning (nil, nil), when no PDF was produced.
func checkPDFPageCount(paths map[engine.Format]string, slideCount int) (*check.Finding, error) {
	pdfPath, ok := paths[engine.FormatPDF]
	if !ok {
		return nil, nil
	}
	return check.ComparePDFPages(pdfPath, slideCount)
}

// speakerNotes composes what each slide carries into a PPTX notes page. The
// image-mode export has no selectable text, so this is the only place the words
// survive — title first, then whatever the author wrote in `notes`.
func speakerNotes(d *deck.Deck) map[int]string {
	notes := make(map[int]string, len(d.Slides))
	for _, s := range d.Slides {
		var parts []string
		if s.Kicker != "" {
			parts = append(parts, s.Kicker)
		}
		if s.Title != "" {
			parts = append(parts, s.Title)
		}
		if s.Notes != "" {
			parts = append(parts, "", s.Notes)
		}
		notes[s.Number] = strings.Join(parts, "\n")
	}
	return notes
}

// renderFormats runs each requested adapter, printing the artifact path it
// produced, and returns the absolute path it wrote for each format. A
// failing adapter aborts the build rather than leaving a partial set of
// artifacts that looks complete.
func renderFormats(ctx context.Context, formats []engine.Format, job engine.Job, notes map[int]string, stdout io.Writer) (map[engine.Format]string, error) {
	paths := make(map[engine.Format]string, len(formats))
	for _, f := range formats {
		r, err := rendererFor(f, notes)
		if err != nil {
			return nil, err
		}
		path, err := r.Render(ctx, job)
		if err != nil {
			return nil, err
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		_, _ = fmt.Fprintln(stdout, abs)
		paths[f] = abs
	}
	return paths, nil
}

func rendererFor(f engine.Format, notes map[int]string) (engine.Renderer, error) {
	switch f {
	case engine.FormatPDF:
		return engine.NewPDFRenderer(), nil
	case engine.FormatPNG:
		return engine.NewPNGRenderer(), nil
	case engine.FormatPPTX:
		r := engine.NewPPTXRenderer()
		r.Notes = notes
		return r, nil
	default:
		return nil, fmt.Errorf("format %q is declared but has no adapter yet", f)
	}
}
