package deck

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"gopkg.in/yaml.v3"
)

// deckFile is the deck-level manifest; slides live beside it.
const deckFile = "deck.yaml"

// slidesDir holds one Markdown file per slide. Lexical filename order is the
// deck order, which is why the convention is an NN- prefix.
const slidesDir = "slides"

// chromeKeys are front-matter keys consumed by Slide's typed fields; everything
// else is layout vocabulary and is left in Fields.
var chromeKeys = []string{"layout", "kicker", "title", "phase", "badge", "notes", "skip"}

// Load reads a deck directory: <root>/deck.yaml plus <root>/slides/*.md.
func Load(root string) (*Deck, error) {
	manifest := filepath.Join(root, deckFile)
	raw, err := os.ReadFile(manifest) //nolint:gosec // the deck root is an operator-supplied flag; reading it is the point
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", manifest, err)
	}

	var d Deck
	if err := yaml.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("parse %s: %w", manifest, err)
	}

	slides, err := loadSlides(root, filepath.Join(root, slidesDir))
	if err != nil {
		return nil, err
	}
	d.Slides = slides
	return &d, nil
}

// loadSlides parses every Markdown file in dir, dropping skipped slides and
// numbering what remains. root is the deck root a slide's rows_csv (and any
// future compiler-resolved path) is resolved against — dir is already one
// level below it.
func loadSlides(root, dir string) ([]Slide, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", dir, err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no slides found in %s", dir)
	}
	sort.Strings(paths)

	slides := make([]Slide, 0, len(paths))
	for _, path := range paths {
		s, err := loadSlide(root, path)
		if err != nil {
			return nil, err
		}
		if s.Skip {
			continue
		}
		s.Number = len(slides) + 1
		slides = append(slides, *s)
	}
	if len(slides) == 0 {
		return nil, fmt.Errorf("every slide in %s is skipped", dir)
	}
	return slides, nil
}

// loadSlide parses one slide file into front-matter plus rendered body.
func loadSlide(root, path string) (*Slide, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // slide paths come from globbing the operator-supplied deck root
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	front, body, err := splitFrontMatter(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	var s Slide
	if err := yaml.Unmarshal(front, &s); err != nil {
		return nil, fmt.Errorf("parse front-matter in %s: %w", path, err)
	}
	fields, err := layoutFields(front)
	if err != nil {
		return nil, fmt.Errorf("parse front-matter in %s: %w", path, err)
	}
	if err := resolveRowsCSV(root, fields); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	s.Fields = fields
	s.Source = path

	html, err := renderMarkdown(body)
	if err != nil {
		return nil, fmt.Errorf("render body of %s: %w", path, err)
	}
	s.Body = html

	if s.Layout == "" {
		return nil, fmt.Errorf("%s: front-matter is missing required key \"layout\"", path)
	}
	return &s, nil
}

// splitFrontMatter separates a leading `---` fenced YAML block from the body.
func splitFrontMatter(raw []byte) (front, body []byte, err error) {
	const fence = "---"
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.HasPrefix(text, fence+"\n") {
		return nil, nil, fmt.Errorf("file must start with a %q front-matter fence", fence)
	}
	rest := text[len(fence)+1:]
	end := strings.Index(rest, "\n"+fence)
	if end < 0 {
		return nil, nil, fmt.Errorf("front-matter fence %q is never closed", fence)
	}
	front = []byte(rest[:end])
	body = []byte(strings.TrimPrefix(rest[end+len(fence)+1:], "\n"))
	return front, body, nil
}

// layoutFields returns the front-matter keys that are not chrome.
func layoutFields(front []byte) (map[string]any, error) {
	all := map[string]any{}
	if err := yaml.Unmarshal(front, &all); err != nil {
		return nil, err
	}
	for _, k := range chromeKeys {
		delete(all, k)
	}
	return all, nil
}

// renderMarkdown converts the slide body to HTML. Slide sources are authored in
// the deck repo, so the output is trusted the same way templates are.
func renderMarkdown(body []byte) (template.HTML, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return "", nil
	}
	var buf bytes.Buffer
	if err := goldmark.New().Convert(body, &buf); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil //nolint:gosec // authored content, same trust boundary as the templates
}
