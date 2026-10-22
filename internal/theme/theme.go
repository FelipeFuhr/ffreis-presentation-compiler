// Package theme loads a brand theme: the CSS, the chrome wrapper and one
// template per layout. The split with the compiler is deliberate — the compiler
// owns the document shell (page geometry, print rules), the theme owns
// everything a designer would want to change.
package theme

import (
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/pathsafe"
)

const (
	manifestFile = "theme.yaml"
	chromeFile   = "slide.gohtml"
	layoutsDir   = "layouts"
	cssDir       = "css"
	assetsDir    = "assets"
	fontsDir     = "fonts"
)

// Theme is a loaded brand theme.
type Theme struct {
	Name string `yaml:"name"`
	// Font families the theme expects; reported by `doctor` and checked at
	// validation time so a missing face fails loudly instead of silently
	// falling back to a system font.
	Fonts []string `yaml:"fonts"`

	// PPTXFont is the typeface a native .pptx should name.
	//
	// Separate from Fonts because the two answer different questions. Fonts is
	// a CSS stack the browser resolves with fallbacks; OOXML names exactly one
	// face and has no fallback, so a theme whose CSS font is not on the
	// reader's machine needs a deliberate second choice. Empty means "use
	// what the render measured", which is right when that face is embedded or
	// is one Office ships.
	PPTXFont string `yaml:"pptx_font"`

	Dir     string
	CSS     string
	chrome  *template.Template
	layouts map[string]*template.Template
}

// Load reads the theme named name from root (root/<name>/…).
func Load(root, name string) (*Theme, error) {
	if name == "" {
		return nil, fmt.Errorf("deck.yaml does not name a theme")
	}
	// A theme name is a single directory component, never a path: reject a
	// separator or ".." outright rather than let it reach the join below.
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return nil, fmt.Errorf("theme %q is not a valid theme name: it must be a single directory name, not a path", name)
	}
	dir, err := pathsafe.Contain(root, name)
	if err != nil {
		return nil, fmt.Errorf("theme %q: %w", name, err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("theme %q: %w", name, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("theme %q: %s is not a directory", name, dir)
	}

	t := &Theme{Dir: dir}
	if err := t.readManifest(); err != nil {
		return nil, err
	}
	if err := t.readCSS(); err != nil {
		return nil, err
	}
	if err := t.readTemplates(); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *Theme) readManifest() error {
	path := filepath.Join(t.Dir, manifestFile)
	raw, err := os.ReadFile(path) //nolint:gosec // theme root is an operator-supplied flag
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(raw, t); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

// readCSS concatenates every stylesheet in css/ in filename order, so a theme
// controls cascade order by naming files 10-tokens.css, 20-base.css, ….
func (t *Theme) readCSS() error {
	dir := filepath.Join(t.Dir, cssDir)
	paths, err := filepath.Glob(filepath.Join(dir, "*.css"))
	if err != nil {
		return fmt.Errorf("scan %s: %w", dir, err)
	}
	if len(paths) == 0 {
		return fmt.Errorf("theme %q has no stylesheets in %s", t.Name, dir)
	}
	sort.Strings(paths)

	var b strings.Builder
	for _, p := range paths {
		raw, err := os.ReadFile(p) //nolint:gosec // discovered under the operator-supplied theme root
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		fmt.Fprintf(&b, "/* %s */\n", filepath.Base(p))
		b.Write(raw)
		b.WriteString("\n")
	}
	t.CSS = b.String()
	return nil
}

func (t *Theme) readTemplates() error {
	chromePath := filepath.Join(t.Dir, chromeFile)
	chrome, err := template.New(chromeFile).Funcs(FuncMap()).ParseFiles(chromePath)
	if err != nil {
		return fmt.Errorf("parse %s: %w", chromePath, err)
	}
	t.chrome = chrome

	dir := filepath.Join(t.Dir, layoutsDir)
	paths, err := filepath.Glob(filepath.Join(dir, "*.gohtml"))
	if err != nil {
		return fmt.Errorf("scan %s: %w", dir, err)
	}
	if len(paths) == 0 {
		return fmt.Errorf("theme %q has no layouts in %s", t.Name, dir)
	}

	t.layouts = make(map[string]*template.Template, len(paths))
	for _, p := range paths {
		name := strings.TrimSuffix(filepath.Base(p), ".gohtml")
		tpl, err := template.New(filepath.Base(p)).Funcs(FuncMap()).ParseFiles(p)
		if err != nil {
			return fmt.Errorf("parse %s: %w", p, err)
		}
		t.layouts[name] = tpl
	}
	return nil
}

// Chrome is the template that wraps every slide.
func (t *Theme) Chrome() *template.Template { return t.chrome }

// Layout returns the template for a layout name.
func (t *Theme) Layout(name string) (*template.Template, error) {
	tpl, ok := t.layouts[name]
	if !ok {
		return nil, fmt.Errorf("theme %q has no layout %q (available: %s)", t.Name, name, strings.Join(t.LayoutNames(), ", "))
	}
	return tpl, nil
}

// LayoutNames lists the layouts this theme provides, sorted.
func (t *Theme) LayoutNames() []string {
	names := make([]string, 0, len(t.layouts))
	for n := range t.layouts {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// AssetsDir and FontsDir are copied next to the rendered HTML so relative URLs
// resolve for both the browser and the PDF print path.
func (t *Theme) AssetsDir() string { return filepath.Join(t.Dir, assetsDir) }

// FontsDir is the theme's self-hosted font directory.
func (t *Theme) FontsDir() string { return filepath.Join(t.Dir, fontsDir) }

// PPTXFontDir is where a theme puts font files for a native .pptx to embed.
//
// Separate from `fonts/`, which holds the woff2 the HTML uses: PowerPoint
// cannot read woff2, so embedding needs sfnt (.ttf/.otf) and a theme that
// wants both ships both.
const PPTXFontDir = "pptx/fonts"

// EmbeddableFont is one sfnt file a theme offers for embedding.
type EmbeddableFont struct {
	// Variant is "regular", "bold", "italic" or "boldItalic", taken from the
	// filename suffix after the last dash.
	Variant string
	Path    string
}

// PPTXFonts lists the sfnt files this theme offers for embedding, keyed by
// variant.
//
// Names are read as `<anything>-<variant>.ttf`, so `Roboto-bold.ttf` is the
// bold variant. A file whose suffix is not a known variant is ignored rather
// than guessed at: embedding the wrong weight is worse than not embedding it.
func (t *Theme) PPTXFonts() ([]EmbeddableFont, error) {
	dir := filepath.Join(t.Dir, PPTXFontDir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("theme: read %s: %w", dir, err)
	}

	known := map[string]string{
		"regular":    "regular",
		"bold":       "bold",
		"italic":     "italic",
		"bolditalic": "boldItalic",
	}
	var out []EmbeddableFont
	for _, e := range entries {
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if e.IsDir() || (ext != ".ttf" && ext != ".otf") {
			continue
		}
		base := strings.TrimSuffix(name, filepath.Ext(name))
		dash := strings.LastIndex(base, "-")
		if dash < 0 {
			continue
		}
		variant, ok := known[strings.ToLower(base[dash+1:])]
		if !ok {
			continue
		}
		out = append(out, EmbeddableFont{Variant: variant, Path: filepath.Join(dir, name)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Variant < out[j].Variant })
	return out, nil
}
