package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/contract"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/gen"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/logx"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/render"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/seo"
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
	fs.StringVar(&o.lang, "lang", "",
		"document language, overriding deck.yaml's `lang:` (default: the deck's own, else "+deck.DefaultLang+")")
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

	// Validate before anything renders: a bad field here is a named finding,
	// not a blank region or a confusing template error three steps downstream.
	// Advisory findings print and do not stop the build — see contract.Fatal.
	if findings := contract.Validate(d, th, opts.deckRoot); len(findings) > 0 {
		_, _ = fmt.Fprint(stderr, contract.Report(findings))
		if contract.Fatal(findings) {
			return exitError
		}
	}

	// Beside the contract check, not after the generators: neither reads
	// generated output, and a published deck with a bad description should fail
	// before anything spends time shelling out to a chart generator.
	if code := reportSEO(d, opts, stdout, stderr); code != exitOK {
		return code
	}

	// Generators run before rendering: each writes an image and sets the
	// slide's media field, so layouts cannot tell a generated chart from an
	// authored one.
	genOpts := gen.Options{DeckRoot: opts.deckRoot, OutDir: opts.out, Tokens: th.Tokens()}
	if err := gen.DefaultRegistry().Apply(context.Background(), engine.ExecRunner, d, genOpts); err != nil {
		_, _ = fmt.Fprintf(stderr, "build: %v\n", err)
		return exitError
	}

	renderOpts := render.Options{
		DeckRoot: opts.deckRoot,
		OutDir:   opts.out,
		Lang:     opts.lang,
		Head:     seo.Head(d.Document(opts.lang), d.SEO),
	}
	if err := render.Render(d, th, renderOpts); err != nil {
		_, _ = fmt.Fprintf(stderr, "build: %v\n", err)
		return exitError
	}
	if err := writeDiscoveryFiles(d, opts, stdout); err != nil {
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
	if err := renderFormats(context.Background(), formats, job, speakerNotes(d), stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "build: %v\n", err)
		return exitError
	}
	return exitOK
}

// reportSEO prints SEO findings and decides whether they are fatal.
//
// There is deliberately no -strict-seo flag: whether a defect matters is
// already answered by the deck itself. A deck that declares a public address
// has an audience and a crawler, so a missing description or a relative
// og:image is a shipping defect and fails the build. A deck with no address is
// an internal document; the same finding is advice, printed once and not in
// the way.
func reportSEO(d *deck.Deck, opts *buildOptions, stdout, stderr io.Writer) int {
	findings := seo.Validate(d.Document(opts.lang), d.SEO)
	if len(findings) == 0 {
		return exitOK
	}
	if d.SEO.Published() {
		_, _ = fmt.Fprint(stderr, seo.Report(findings))
		_, _ = fmt.Fprintf(stderr, "build: refusing to publish %s with the findings above\n", d.SEO.Canonical())
		return exitError
	}
	_, _ = fmt.Fprint(stdout, seo.Report(findings))
	return exitOK
}

// writeDiscoveryFiles emits sitemap.xml and robots.txt for a published deck.
func writeDiscoveryFiles(d *deck.Deck, opts *buildOptions, stdout io.Writer) error {
	written, err := seo.Write(opts.out, d.SEO)
	if err != nil {
		return err
	}
	for _, p := range written {
		abs, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, abs)
	}
	return nil
}

// renderFormats runs each requested adapter, printing the artifact path it
// produced. A failing adapter aborts the build rather than leaving a partial
// set of artifacts that looks complete.
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

func renderFormats(ctx context.Context, formats []engine.Format, job engine.Job, notes map[int]string, stdout io.Writer) error {
	for _, f := range formats {
		r, err := rendererFor(f, notes)
		if err != nil {
			return err
		}
		path, err := r.Render(ctx, job)
		if err != nil {
			return err
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, abs)
	}
	return nil
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
