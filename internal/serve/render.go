package serve

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/gen"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/render"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/theme"
)

// renderResult is one successful render. html is exactly what render.Render
// wrote to outDir/index.html — the reload script is injected by the caller,
// never here, so the file on disk always matches what `build` would produce.
type renderResult struct {
	html       []byte
	slideCount int
	themeName  string
}

// renderOnce loads the deck and theme, runs any chart/diagram generators,
// and renders into outDir. This is the same pipeline `build` uses, so the
// preview shows what `build` would actually produce.
func renderOnce(opts Options, outDir string) (renderResult, error) {
	d, err := deck.Load(opts.DeckRoot)
	if err != nil {
		return renderResult{}, err
	}
	th, err := theme.Load(opts.ThemeRoot, d.Theme)
	if err != nil {
		return renderResult{}, err
	}

	// Generators run before rendering, same as build: each writes an image
	// and sets the slide's media field, so layouts cannot tell a generated
	// chart from an authored one.
	genOpts := gen.Options{DeckRoot: opts.DeckRoot, OutDir: outDir, Tokens: th.Tokens()}
	if err := gen.DefaultRegistry().Apply(context.Background(), engine.ExecRunner, d, genOpts); err != nil {
		return renderResult{}, err
	}

	renderOpts := render.Options{DeckRoot: opts.DeckRoot, OutDir: outDir, Lang: opts.Lang}
	if err := render.Render(d, th, renderOpts); err != nil {
		return renderResult{}, err
	}

	index := filepath.Join(outDir, render.IndexFile)
	html, err := os.ReadFile(index) //nolint:gosec // outDir is our own temp directory, not external input
	if err != nil {
		return renderResult{}, fmt.Errorf("read rendered %s: %w", render.IndexFile, err)
	}
	return renderResult{html: html, slideCount: len(d.Slides), themeName: d.Theme}, nil
}
