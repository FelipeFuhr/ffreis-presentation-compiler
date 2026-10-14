// Package render turns a loaded deck plus a theme into a self-contained HTML
// directory. It is the only stage that knows about HTML; the PDF and PNG
// adapters consume what it writes.
package render

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/theme"
)

//go:embed base.gohtml
var baseTemplate string

// IndexFile is the entry point written into the output directory.
const IndexFile = "index.html"

// SlideContext is what chrome and layout templates are executed against.
type SlideContext struct {
	Deck  *deck.Deck
	Slide deck.Slide
	Size  deck.Size

	// Fields and Body are aliases of the slide's own values, hoisted so
	// layouts read `.Fields.rows` rather than `.Slide.Fields.rows`.
	Fields map[string]any
	Body   template.HTML

	// Content is the rendered layout, set only when executing chrome.
	Content template.HTML
}

type documentContext struct {
	Deck     *deck.Deck
	Size     deck.Size
	Lang     string
	CSS      template.CSS
	Head     template.HTML
	Sections []template.HTML
}

// Options controls a render.
type Options struct {
	DeckRoot string
	OutDir   string
	Lang     string

	// Head is extra markup for the document head — the SEO meta block. It is
	// passed in already rendered rather than built here so this package stays
	// ignorant of the discovery vocabulary; its job is page geometry.
	Head template.HTML
}

// Render writes index.html plus copied assets into opts.OutDir.
func Render(d *deck.Deck, th *theme.Theme, opts Options) error {
	size, err := d.Size()
	if err != nil {
		return err
	}

	sections, err := renderSlides(d, th, size)
	if err != nil {
		return err
	}

	base, err := template.New("base").Parse(baseTemplate)
	if err != nil {
		return fmt.Errorf("parse document shell: %w", err)
	}

	lang := d.Language(opts.Lang)
	var out bytes.Buffer
	//nolint:gosec // G203: the stylesheet is theme-authored source read from the
	// operator's own theme root — the same trust boundary as the templates it
	// styles. It is never derived from deck content or any external input.
	css := template.CSS(th.CSS)
	ctx := documentContext{Deck: d, Size: size, Lang: lang, CSS: css, Head: opts.Head, Sections: sections}
	if err := base.Execute(&out, ctx); err != nil {
		return fmt.Errorf("render document shell: %w", err)
	}

	if err := os.MkdirAll(opts.OutDir, 0o750); err != nil {
		return fmt.Errorf("create %s: %w", opts.OutDir, err)
	}
	index := filepath.Join(opts.OutDir, IndexFile)
	if err := os.WriteFile(index, out.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", index, err)
	}
	return copyAssets(th, opts)
}

// renderSlides executes each slide's layout, then wraps it in the theme chrome.
func renderSlides(d *deck.Deck, th *theme.Theme, size deck.Size) ([]template.HTML, error) {
	sections := make([]template.HTML, 0, len(d.Slides))
	for _, s := range d.Slides {
		ctx := SlideContext{Deck: d, Slide: s, Size: size, Fields: s.Fields, Body: s.Body}

		content, err := executeSlide(th, s, ctx)
		if err != nil {
			return nil, err
		}
		ctx.Content = content

		var wrapped bytes.Buffer
		if err := th.Chrome().Execute(&wrapped, ctx); err != nil {
			return nil, fmt.Errorf("slide %d (%s): render chrome: %w", s.Number, s.Source, err)
		}
		sections = append(sections, template.HTML(wrapped.String())) //nolint:gosec // theme-authored templates, trusted like any template output
	}
	return sections, nil
}

func executeSlide(th *theme.Theme, s deck.Slide, ctx SlideContext) (template.HTML, error) {
	layout, err := th.Layout(s.Layout)
	if err != nil {
		return "", fmt.Errorf("slide %d (%s): %w", s.Number, s.Source, err)
	}
	var buf bytes.Buffer
	if err := layout.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("slide %d (%s): render layout %q: %w", s.Number, s.Source, s.Layout, err)
	}
	return template.HTML(buf.String()), nil //nolint:gosec // theme-authored templates
}

// copyAssets lays the theme's fonts and assets beside index.html, then the
// deck's own assets on top, so a deck can shadow a theme asset by name.
func copyAssets(th *theme.Theme, opts Options) error {
	jobs := []struct{ src, dst string }{
		{th.FontsDir(), filepath.Join(opts.OutDir, "fonts")},
		{th.AssetsDir(), filepath.Join(opts.OutDir, "assets")},
		{filepath.Join(opts.DeckRoot, "assets"), filepath.Join(opts.OutDir, "assets")},
	}
	for _, j := range jobs {
		if err := copyDir(j.src, j.dst); err != nil {
			return err
		}
	}
	return nil
}

// copyDir mirrors src into dst. A missing source is not an error: a theme
// without fonts, or a deck without assets, is legitimate.
func copyDir(src, dst string) error {
	info, err := os.Stat(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", src)
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	for _, e := range entries {
		from, to := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyDir(from, to); err != nil {
				return err
			}
			continue
		}
		// e.IsDir() uses Lstat semantics: a symlink pointing at a directory
		// reports false here, same as a regular file. Left unhandled, it
		// would fall through to copyFile, which opens it fine (os.Open
		// follows symlinks) but then fails on the copy with a low-level
		// "is a directory" error that names neither the symlink nor why. A
		// symlink loop takes the same path — the loop-back entry's IsDir()
		// is also false — so it fails immediately here rather than
		// recursing unboundedly; there is no loop to guard against. Name the
		// real problem instead of letting copyFile's error stand in for it.
		if e.Type()&fs.ModeSymlink != 0 {
			if target, err := os.Stat(from); err == nil && target.IsDir() {
				return fmt.Errorf("%s is a symlink to a directory, which is not supported here; "+
					"point the theme or deck asset at a real directory instead", from)
			}
		}
		if err := copyFile(from, to); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from) //nolint:gosec // paths are walked from operator-supplied roots
	if err != nil {
		return fmt.Errorf("open %s: %w", from, err)
	}
	defer func() { _ = in.Close() }() // read-only handle; a close error cannot lose data

	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // destination is under the operator-supplied out dir
	if err != nil {
		return fmt.Errorf("create %s: %w", to, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copy %s: %w", from, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close %s: %w", to, err)
	}
	return nil
}
