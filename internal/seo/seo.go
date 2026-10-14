// Package seo emits the discovery surface for a deck that is published on the
// web: the document head's meta vocabulary, a schema.org description of the
// deck, a sitemap and a robots policy.
//
// The vocabulary and its emission order are taken from the website compiler's
// consumers, so a deck and a site published under the same brand present
// identically to a crawler or a link unfurler. Two things are deliberately
// different.
//
// **A deck defaults to noindex, nofollow.** A website wants to be found; a
// deck is usually unpublished client material that happens to be HTML, and the
// safe default is the opposite one. The full indexable surface switches on
// only when the author sets `base_url` — an explicit statement that this deck
// has a public address. Everything else degrades to a title and a noindex.
//
// **The JSON-LD is marshalled, not interpolated.** Every site in the fleet
// builds its ld+json by substituting values into a template literal, so a
// description containing a quote emits a block no parser accepts — silently,
// because nothing validates it. encoding/json cannot make that mistake.
package seo

import (
	"fmt"
	"html"
	"html/template"
	"strings"
)

// Defaults. RobotsPrivate is the default policy for the reason above.
const (
	RobotsPrivate = "noindex, nofollow"
	RobotsPublic  = "index, follow, max-image-preview:large"
	defaultCard   = "summary_large_image"
	defaultOGType = "article"
	sitemapFile   = "sitemap.xml"
	robotsFile    = "robots.txt"
	// An XML namespace name, not an address: nothing dereferences it, and the
	// sitemaps protocol fixes it to this exact string — an https spelling is a
	// different namespace and makes the document invalid. The scanners read it
	// as insecure HTTP regardless, so the suppression is here with its reason
	// rather than on the three call sites.
	sitemapXMLNS    = "http://www.sitemaps.org/schemas/sitemap/0.9" // NOSONAR go:S5332
	schemaContext   = "https://schema.org"
	schemaType      = "PresentationDigitalDocument"
	schemaOrgType   = "Organization"
	schemaImageType = "ImageObject"
)

// Length bounds. Search engines truncate past these, so a title or
// description that exceeds one is reported — a check no tool in this fleet
// performs today, which is why descriptions have drifted to 300 characters
// elsewhere.
const (
	MaxTitleChars  = 60
	MinDescChars   = 50
	MaxDescChars   = 160
	MaxRobotsChars = 120
)

// Meta is the deck.yaml `seo:` block.
type Meta struct {
	// BaseURL is the deck's public origin, e.g. "https://example.com". Setting
	// it is what declares the deck published; leaving it empty keeps the deck
	// private and emits only a title and a noindex.
	BaseURL string `yaml:"base_url"`
	// Path is the deck's path under BaseURL, e.g. "/decks/q3-review/". The
	// canonical URL is composed from the two rather than written out per deck,
	// so it cannot drift from the address the deck is actually served at.
	Path string `yaml:"path"`

	Description  string   `yaml:"description"`
	Robots       string   `yaml:"robots"`
	Organization string   `yaml:"organization"`
	Locale       string   `yaml:"locale"`
	Image        string   `yaml:"image"`
	ImageAlt     string   `yaml:"image_alt"`
	TwitterCard  string   `yaml:"twitter_card"`
	SameAs       []string `yaml:"same_as"`
	Keywords     []string `yaml:"keywords"`

	// DatePublished is an ISO 8601 date. Named for the schema.org property it
	// becomes, so the mapping is obvious at the call site; `Published()` below
	// is the unrelated question of whether the deck has a public address.
	DatePublished string `yaml:"date_published"`
}

// Document is what the deck contributes. Kept separate from deck.Deck so this
// package stays a leaf and the content model can depend on it.
type Document struct {
	Title    string
	Subtitle string
	Lang     string
	Slides   int
}

// Published reports whether the deck declares a public address, which is the
// switch between the two modes described in the package comment.
func (m Meta) Published() bool { return strings.TrimSpace(m.BaseURL) != "" }

// Canonical composes the deck's absolute URL. Empty when the deck is private.
func (m Meta) Canonical() string {
	if !m.Published() {
		return ""
	}
	return strings.TrimSuffix(m.BaseURL, "/") + "/" + strings.TrimPrefix(m.Path, "/")
}

// robots resolves the policy, defaulting to private.
func (m Meta) robots() string {
	if r := strings.TrimSpace(m.Robots); r != "" {
		return r
	}
	return RobotsPrivate
}

// indexable reports whether the resolved policy permits indexing. Used to keep
// robots.txt consistent with the meta tag, which is the pair that most often
// disagrees in practice.
func (m Meta) indexable() bool {
	return !strings.Contains(strings.ToLower(m.robots()), "noindex")
}

// description falls back to the deck subtitle, which is the one field an author
// has already written for human readers.
func (m Meta) description(d Document) string {
	if s := strings.TrimSpace(m.Description); s != "" {
		return s
	}
	return strings.TrimSpace(d.Subtitle)
}

// locale converts a BCP 47 document language to the underscored form Open
// Graph expects ("pt-BR" -> "pt_BR").
func (m Meta) locale(d Document) string {
	if l := strings.TrimSpace(m.Locale); l != "" {
		return l
	}
	return strings.ReplaceAll(strings.TrimSpace(d.Lang), "-", "_")
}

// imageURL absolutises the social image against the deck's origin. A relative
// og:image is ignored by every unfurler, which is why this is resolved here
// rather than left to the author.
func (m Meta) imageURL() string {
	img := strings.TrimSpace(m.Image)
	if img == "" || !m.Published() {
		return ""
	}
	if strings.HasPrefix(img, "http://") || strings.HasPrefix(img, "https://") {
		return img
	}
	return strings.TrimSuffix(m.BaseURL, "/") + "/" + strings.TrimPrefix(img, "/")
}

// Head renders the meta block that belongs in <head>, after the title.
func Head(d Document, m Meta) template.HTML {
	var b strings.Builder
	meta(&b, "name", "robots", m.robots())
	meta(&b, "name", "description", m.description(d))
	meta(&b, "name", "author", m.Organization)

	if m.Published() {
		writeLink(&b, "canonical", m.Canonical())
		writeOpenGraph(&b, d, m)
		writeTwitter(&b, d, m)
	}
	b.WriteString(jsonLD(d, m))

	//nolint:gosec // G203: every value above goes through html.EscapeString or
	// encoding/json; this is the assembled markup, not interpolated input.
	return template.HTML(b.String())
}

func writeOpenGraph(b *strings.Builder, d Document, m Meta) {
	meta(b, "property", "og:locale", m.locale(d))
	meta(b, "property", "og:type", defaultOGType)
	meta(b, "property", "og:title", d.Title)
	meta(b, "property", "og:description", m.description(d))
	meta(b, "property", "og:url", m.Canonical())
	meta(b, "property", "og:image", m.imageURL())
	meta(b, "property", "og:image:alt", m.ImageAlt)
	meta(b, "property", "og:site_name", m.Organization)
}

func writeTwitter(b *strings.Builder, d Document, m Meta) {
	card := strings.TrimSpace(m.TwitterCard)
	if card == "" {
		card = defaultCard
	}
	meta(b, "name", "twitter:card", card)
	meta(b, "name", "twitter:title", d.Title)
	meta(b, "name", "twitter:description", m.description(d))
	meta(b, "name", "twitter:image", m.imageURL())
	meta(b, "name", "twitter:image:alt", m.ImageAlt)
}

// meta writes one tag, or nothing when the value is empty. An empty
// `content=""` is worse than an absent tag: validators report it as a defect
// and crawlers treat it as an assertion that the field is blank.
//
// The attribute name is a parameter because the two vocabularies disagree:
// og:* is addressed by `property`, twitter:* by `name`. Emitting og:* under
// `name` is the single most common mistake in hand-written head blocks.
func meta(b *strings.Builder, attr, key, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	fmt.Fprintf(b, "<meta %s=%q content=%q>\n", attr, key, html.EscapeString(value))
}

func writeLink(b *strings.Builder, rel, href string) {
	if strings.TrimSpace(href) == "" {
		return
	}
	fmt.Fprintf(b, "<link rel=%q href=%q>\n", rel, html.EscapeString(href))
}
