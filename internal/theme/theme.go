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
