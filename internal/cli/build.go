package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/check"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/contract"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine/chrome"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine/pptx"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/gen"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/logx"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/pptxlayout"
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
	pptxMode  string
	pptxFont  string
	embedFont bool
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
	fs.StringVar(&o.formats, "formats", "html,pdf", "comma-separated outputs: html, pdf, png, pptx")
	fs.StringVar(&o.pptxFont, "pptx-font", "",
		"typeface every pptx run names; empty uses the theme's pptx_font, then what the render measured")
	fs.BoolVar(&o.embedFont, "pptx-embed-fonts", false,
		"ship the theme's pptx/fonts/*.ttf inside the package (UNVERIFIED: PowerPoint writes EOT, "+
			"this writes sfnt, and no reader available here honours embedded pptx fonts at all)")
	fs.StringVar(&o.pptxMode, "pptx-mode", string(pptxlayout.ModeNative),
		"pptx shape mode: native (editable; geometry from the theme's pptx/ specs) or image (exact; one picture per slide)")
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

	pptxMode, err := pptxlayout.ParseMode(opts.pptxMode)
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
	job := engine.Job{
		IndexPath: index, OutDir: opts.out, Slug: opts.slug, Size: size,
		// Resolved, not the raw flag: the deck's own lang: is what an export
		// should carry when the invocation names none.
		Lang: d.Language(opts.lang),
	}
	font := resolvePPTXFont(opts.pptxFont, th)

	shapes, notes, err := nativeShapes(context.Background(), pptxMode, d, th,
		opts.deckRoot, index, size, wants(formats, engine.FormatPPTX), font)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "build: %v\n", err)
		return exitError
	}

	// With no typeface declared, the theme's fontScheme takes the one the runs
	// actually use -- which measurement read from the CSS. Without this the
	// scheme fell back to DefaultFont while the runs named the measured face,
	// so the package disagreed with itself about its own typeface.
	if font == "" {
		font = dominantFont(shapes)
	}

	embedded, fontNotes, err := loadEmbeddedFonts(opts.embedFont, th, font)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "build: %v\n", err)
		return exitError
	}
	for _, note := range append(fontNotes, notes...) {
		logger.Info("pptx", "note", note)
	}

	paths, err := renderFormats(context.Background(), formats, job,
		pptxOptions{notes: speakerNotes(d), shapes: shapes, font: font, fonts: embedded}, stdout)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "build: %v\n", err)
		return exitError
	}

	// Say what a reader will actually see. A typeface that is neither embedded
	// nor one Office ships substitutes silently on someone else's machine,
	// which is the one thing about a .pptx that cannot be checked by looking
	// at it here -- so it is reported rather than left to be discovered.
	if pptxPath, ok := paths[engine.FormatPPTX]; ok {
		reports, err := pptx.Fonts(pptxPath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "build: %v\n", err)
			return exitError
		}
		for _, r := range reports {
			if r.Safe() {
				logger.Info("pptx", "font", r.String())
				continue
			}
			// Loud, and on stderr: this is the difference between the deck the
			// author saw and the deck the reader gets.
			_, _ = fmt.Fprintf(stderr, "build: warning: %s\n", r)
		}
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

// renderFormats runs each requested adapter, printing the artifact path it
// produced, and returns the absolute path it wrote for each format. A
// failing adapter aborts the build rather than leaving a partial set of
// artifacts that looks complete.
// pptxOptions is what the PPTX adapter needs beyond the job. Bundled rather
// than added as parameters: both are per-slide maps keyed from 1, and a third
// positional map[int]... argument is exactly how they get swapped by mistake.
type pptxOptions struct {
	notes  map[int]string
	shapes map[int][]pptx.Shape
	font   string
	fonts  []pptx.EmbeddedFont
}

func renderFormats(ctx context.Context, formats []engine.Format, job engine.Job, opt pptxOptions, stdout io.Writer) (map[engine.Format]string, error) {
	paths := make(map[engine.Format]string, len(formats))
	for _, f := range formats {
		r, err := rendererFor(f, opt)
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

// dominantFont is the typeface most of a deck's shapes name.
//
// Most, not first: a deck whose body is one face and whose kickers are another
// should declare the body face in its theme, since that is what a reader
// changing the theme font would expect to affect. Ties go to the
// alphabetically first name so the output stays reproducible.
func dominantFont(shapes map[int][]pptx.Shape) string {
	count := map[string]int{}
	for _, slide := range shapes {
		for _, sh := range slide {
			if f := strings.TrimSpace(sh.Style.Font); f != "" {
				count[f]++
			}
		}
	}
	best, bestN := "", 0
	for face, n := range count {
		if n > bestN || (n == bestN && face < best) {
			best, bestN = face, n
		}
	}
	return best
}

// resolvePPTXFont decides the one typeface a .pptx can name.
//
// The flag wins, then the theme's own declaration, then empty -- which leaves
// each run with whatever the render measured. Empty is the right default: a
// theme whose CSS face is safe (embedded, or one Office ships) should not have
// to restate it.
func resolvePPTXFont(flag string, th *theme.Theme) string {
	if strings.TrimSpace(flag) != "" {
		return strings.TrimSpace(flag)
	}
	if th != nil {
		return strings.TrimSpace(th.PPTXFont)
	}
	return ""
}

// loadEmbeddedFonts reads the theme's sfnt files for embedding.
//
// Reports rather than fails when asked to embed a theme that ships no font
// files: the request is a preference, and refusing to build over it would be
// worse than building a deck whose typeface may substitute -- which is the
// state every deck was in before this existed, and which Fonts() names.
func loadEmbeddedFonts(embed bool, th *theme.Theme, typeface string) ([]pptx.EmbeddedFont, []string, error) {
	if !embed || th == nil {
		return nil, nil, nil
	}
	files, err := th.PPTXFonts()
	if err != nil {
		return nil, nil, err
	}
	if len(files) == 0 {
		return nil, []string{fmt.Sprintf(
			"-pptx-embed-fonts was asked for but theme %q ships no sfnt files in %s, so nothing was embedded",
			th.Name, theme.PPTXFontDir)}, nil
	}

	face := typeface
	if face == "" {
		// Embedding needs a name to declare, and an unnamed embedded font is
		// one nothing can reference.
		face = pptx.DefaultFont
		if len(th.Fonts) > 0 && strings.TrimSpace(th.Fonts[0]) != "" {
			face = strings.TrimSpace(th.Fonts[0])
		}
	}

	out := pptx.EmbeddedFont{Typeface: face}
	var loaded []string
	for _, f := range files {
		body, err := os.ReadFile(f.Path)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", f.Path, err)
		}
		switch f.Variant {
		case "regular":
			out.Regular = body
		case "bold":
			out.Bold = body
		case "italic":
			out.Italic = body
		case "boldItalic":
			out.BoldItalic = body
		}
		loaded = append(loaded, f.Variant)
	}
	return []pptx.EmbeddedFont{out}, []string{fmt.Sprintf(
		"embedding %s (%s) so the typeface cannot substitute", face, strings.Join(loaded, ", "))}, nil
}

// wants reports whether f is among the requested formats.
func wants(formats []engine.Format, f engine.Format) bool {
	for _, got := range formats {
		if got == f {
			return true
		}
	}
	return false
}

// measureSlides is a variable for the same reason rendererFor is: it is a
// browser boundary, and every test that drives `build` must be able to run
// without Chrome deciding whether it passes.
var measureSlides = defaultMeasureSlides

func defaultMeasureSlides(ctx context.Context, pageURL string, selectors []string) (map[int]pptxlayout.SlideMeasurement, error) {
	return pptxlayout.Measure(ctx, chrome.New(), pageURL, selectors)
}

// nativeShapes maps each slide onto its layout's PPTX spec, when the theme has
// one and the mode asks for it.
//
// A slide with no entry in the returned map keeps its full-bleed image. That
// is the whole fallback story: a layout the theme has not described, or one
// whose rows are shaped in a way this mapper cannot order into a table, yields
// a correct-but-flat slide rather than a build failure or a wrong table. A
// deck is therefore allowed to be partly editable while specs are written.
func nativeShapes(ctx context.Context, mode pptxlayout.Mode, d *deck.Deck, th *theme.Theme,
	deckRoot, index string, size deck.Size, wantPPTX bool, font string) (map[int][]pptx.Shape, []string, error) {
	// No .pptx requested means no shapes are needed, and measuring would
	// launch a browser for an artifact nobody asked for.
	if mode != pptxlayout.ModeNative || !wantPPTX {
		return nil, nil, nil
	}
	specs, err := pptxlayout.Load(th.Dir)
	if err != nil {
		return nil, nil, err
	}
	if len(specs) == 0 {
		// Nothing described: native mode degrades to image mode wholesale, and
		// says so rather than quietly producing the same file as -pptx-mode=image.
		return nil, []string{fmt.Sprintf(
			"theme %q describes no pptx layouts (%s/%s/), so every slide falls back to its image; "+
				"add one per layout to make slides editable", th.Name, th.Dir, pptxlayout.Dir)}, nil
	}

	// Geometry comes from the browser that laid the slide out. Hand-written
	// rectangles cannot know where a line of text ends or how tall a table
	// row is, which is what put the first version of these specs 14-18% off
	// on tables.
	var measured map[int]pptxlayout.SlideMeasurement
	selectors := pptxlayout.Selectors(specs)
	if len(selectors) > 0 {
		measured, err = measureSlides(ctx, engine.FileURL(index), selectors)
		if err != nil {
			// Falling back wholesale, not partly: a spec written against
			// measurement has no usable geometry without it, and half-placed
			// shapes would be worse than the flat image this returns to.
			return nil, []string{fmt.Sprintf(
				"could not measure the rendered deck, so every slide falls back to its image: %v", err)}, nil
		}
	}

	opt := pptxlayout.Options{
		Font:     font,
		Deck:     d,
		DeckRoot: deckRoot,
		ThemeDir: th.Dir,
		Tokens:   th.Tokens(),
		WidthIn:  size.WidthIn,
		HeightIn: size.HeightIn,
	}

	shapes := map[int][]pptx.Shape{}
	var notes []string
	var undescribed []string
	seen := map[string]bool{}
	for _, s := range d.Slides {
		spec, ok := specs[s.Layout]
		if !ok {
			if !seen[s.Layout] {
				seen[s.Layout] = true
				undescribed = append(undescribed, s.Layout)
			}
			continue
		}
		opt.Measured = measured[s.Number]
		built, slideNotes, err := pptxlayout.Shapes(spec, s, opt)
		if err != nil {
			return nil, nil, err
		}
		notes = append(notes, slideNotes...)
		if len(built) > 0 {
			shapes[s.Number] = built
		}
	}
	if len(undescribed) > 0 {
		sort.Strings(undescribed)
		notes = append(notes, fmt.Sprintf(
			"no pptx layout for %s, so those slides keep their rendered image",
			strings.Join(undescribed, ", ")))
	}
	notes = append(notes, fmt.Sprintf("%d of %d slides are editable", len(shapes), len(d.Slides)))
	return shapes, notes, nil
}

// rendererFor is a variable, not a plain function, for the same reason
// engine.LookPath is: it is the one boundary in this file that needs a browser.
// Stubbing it lets a test drive `build` all the way through the pdf-pages
// comparison — the rule whose entire purpose is to notice a PDF that paginated
// wrong — without a real Chrome deciding whether the test passes.
var rendererFor = defaultRendererFor

func defaultRendererFor(f engine.Format, opt pptxOptions) (engine.Renderer, error) {
	switch f {
	case engine.FormatPDF:
		return engine.NewPDFRenderer(), nil
	case engine.FormatPNG:
		return engine.NewPNGRenderer(), nil
	case engine.FormatPPTX:
		r := engine.NewPPTXRenderer()
		r.Notes = opt.notes
		r.Shapes = opt.shapes
		r.Font = opt.font
		r.EmbeddedFonts = opt.fonts
		return r, nil
	default:
		return nil, fmt.Errorf("format %q is declared but has no adapter yet", f)
	}
}
