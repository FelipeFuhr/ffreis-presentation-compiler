package seo

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Element order in a sitemap is fixed by the schema, so it is the struct field
// order here. The namespace and the tag names match the website compiler's
// output exactly, since both end up in the same search index.
type urlSet struct {
	XMLName xml.Name   `xml:"urlset"`
	Xmlns   string     `xml:"xmlns,attr"`
	URLs    []urlEntry `xml:"url"`
}

type urlEntry struct {
	Loc     string `xml:"loc"`
	Lastmod string `xml:"lastmod,omitempty"`
}

// Sitemap renders a sitemap for the deck, which is a single URL. Returns nil
// when the deck is private: a sitemap listing a page that says noindex is a
// contradiction a crawler reports rather than resolves.
func Sitemap(m Meta) ([]byte, error) {
	if !m.Published() || !m.indexable() {
		return nil, nil
	}
	doc := urlSet{
		Xmlns: sitemapXMLNS,
		URLs:  []urlEntry{{Loc: m.Canonical(), Lastmod: strings.TrimSpace(m.DatePublished)}},
	}
	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode sitemap: %w", err)
	}
	return append([]byte(xml.Header), append(body, '\n')...), nil
}

// Robots renders a robots.txt consistent with the meta policy.
//
// The two disagreeing is the most common SEO defect in this fleet — a page
// that says `index` behind a `Disallow: /`, or the reverse — so they are
// derived from one value here instead of maintained as separate files.
//
// The `Sitemap:` directive is emitted even under a blanket disallow. It is
// inert while crawling is blocked and correct the moment it is not, which is
// the convention the fleet's sites converged on after getting it wrong twice.
func Robots(m Meta) []byte {
	if !m.Published() {
		return nil
	}
	var b strings.Builder
	b.WriteString("User-agent: *\n")
	if m.indexable() {
		b.WriteString("Allow: /\n")
	} else {
		b.WriteString("Disallow: /\n")
	}
	fmt.Fprintf(&b, "\nSitemap: %s/%s\n", strings.TrimSuffix(m.BaseURL, "/"), sitemapFile)
	return []byte(b.String())
}

// Write lays sitemap.xml and robots.txt beside the rendered deck and returns
// the paths it wrote. A private deck produces neither, and that is not an
// error — it is the default.
func Write(outDir string, m Meta) ([]string, error) {
	sitemap, err := Sitemap(m)
	if err != nil {
		return nil, err
	}
	files := []struct {
		name string
		body []byte
	}{
		{sitemapFile, sitemap},
		{robotsFile, Robots(m)},
	}

	var written []string
	for _, f := range files {
		if f.body == nil {
			continue
		}
		path := filepath.Join(outDir, f.name)
		if err := os.WriteFile(path, f.body, 0o600); err != nil {
			return nil, fmt.Errorf("write %s: %w", path, err)
		}
		written = append(written, path)
	}
	return written, nil
}
