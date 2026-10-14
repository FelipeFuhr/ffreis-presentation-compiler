package seo_test

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/seo"
)

func doc() seo.Document {
	return seo.Document{
		Title:    "Quarterly Operations Review",
		Subtitle: "Throughput, cost and the plan for next quarter",
		Lang:     "pt-BR",
		Slides:   7,
	}
}

// published returns a Meta that passes Validate cleanly, so a test that wants
// one specific defect can introduce exactly that one.
func published() seo.Meta {
	return seo.Meta{
		BaseURL:      "https://example.com",
		Path:         "/decks/q3/",
		Robots:       seo.RobotsPublic,
		Organization: "Sample Industries",
		Description:  "How throughput, unit cost and headcount moved last quarter, and what changes next.",
		Image:        "/assets/og.png",
		ImageAlt:     "Cover slide",
	}
}

func TestPrivateIsTheDefault(t *testing.T) {
	// A deck with no seo: block at all must not be indexable. This is the one
	// behaviour that differs from the website compiler on purpose: a deck is
	// usually unpublished material that happens to be HTML.
	head := string(seo.Head(doc(), seo.Meta{}))

	if !strings.Contains(head, `<meta name="robots" content="noindex, nofollow">`) {
		t.Errorf("a deck with no seo block is not noindex:\n%s", head)
	}
	for _, absent := range []string{"canonical", "og:url", "twitter:card", "og:image"} {
		if strings.Contains(head, absent) {
			t.Errorf("a private deck emitted %q, which only makes sense with a public address", absent)
		}
	}
}

func TestPrivateDeckWritesNoDiscoveryFiles(t *testing.T) {
	out := t.TempDir()

	written, err := seo.Write(out, seo.Meta{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("Write produced %v for a private deck", written)
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("the output directory is not empty: %v", entries)
	}
}

func TestHeadEmitsTheFleetVocabulary(t *testing.T) {
	head := string(seo.Head(doc(), published()))

	// The attribute each vocabulary is addressed by differs, and emitting
	// og:* under `name` is the most common hand-written head-block mistake.
	want := []string{
		`<meta name="robots" content="index, follow, max-image-preview:large">`,
		`<meta name="description" content="How throughput, unit cost and headcount moved last quarter, and what changes next.">`,
		`<meta name="author" content="Sample Industries">`,
		`<link rel="canonical" href="https://example.com/decks/q3/">`,
		`<meta property="og:locale" content="pt_BR">`,
		`<meta property="og:type" content="article">`,
		`<meta property="og:title" content="Quarterly Operations Review">`,
		`<meta property="og:url" content="https://example.com/decks/q3/">`,
		`<meta property="og:image" content="https://example.com/assets/og.png">`,
		`<meta property="og:image:alt" content="Cover slide">`,
		`<meta property="og:site_name" content="Sample Industries">`,
		`<meta name="twitter:card" content="summary_large_image">`,
		`<meta name="twitter:title" content="Quarterly Operations Review">`,
		`<meta name="twitter:image" content="https://example.com/assets/og.png">`,
		`<meta name="twitter:image:alt" content="Cover slide">`,
	}
	for _, w := range want {
		if !strings.Contains(head, w) {
			t.Errorf("head is missing:\n  %s\ngot:\n%s", w, head)
		}
	}
}

func TestHeadNeverEmitsAnEmptyTag(t *testing.T) {
	// An empty content="" is worse than an absent tag: validators report it,
	// and a crawler reads it as an assertion that the field is blank.
	head := string(seo.Head(seo.Document{Title: "T", Lang: "en"}, seo.Meta{BaseURL: "https://example.com"}))

	if m := regexp.MustCompile(`content=""`).FindString(head); m != "" {
		t.Errorf("head contains an empty content attribute:\n%s", head)
	}
}

func TestDescriptionFallsBackToTheSubtitle(t *testing.T) {
	m := published()
	m.Description = ""

	head := string(seo.Head(doc(), m))

	if !strings.Contains(head, `content="Throughput, cost and the plan for next quarter"`) {
		t.Errorf("description did not fall back to the deck subtitle:\n%s", head)
	}
}

func TestSocialImageIsAbsolutised(t *testing.T) {
	cases := map[string]string{
		"/assets/og.png":                 "https://example.com/assets/og.png",
		"assets/og.png":                  "https://example.com/assets/og.png",
		"https://cdn.example.com/og.png": "https://cdn.example.com/og.png",
	}
	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			m := published()
			m.Image = input

			head := string(seo.Head(doc(), m))

			if !strings.Contains(head, `<meta property="og:image" content="`+want+`">`) {
				t.Errorf("og:image for %q was not %q:\n%s", input, want, head)
			}
		})
	}
}

// TestHeadEscapesAttributeValues covers the injection that string
// concatenation invites: a title with a quote in it would otherwise close the
// attribute and emit arbitrary markup.
func TestHeadEscapesAttributeValues(t *testing.T) {
	d := doc()
	d.Title = `Q3 "review" <b>& more</b>`

	head := string(seo.Head(d, published()))

	if strings.Contains(head, `content="Q3 "review"`) {
		t.Errorf("an unescaped quote closed the attribute early:\n%s", head)
	}
	if !strings.Contains(head, "&#34;review&#34;") || !strings.Contains(head, "&lt;b&gt;") {
		t.Errorf("title was not escaped into the attribute:\n%s", head)
	}
}

// TestJSONLDIsValidJSONWithAwkwardText is the reason this package marshals
// rather than interpolating. Every site in the fleet builds its ld+json by
// substituting into a template literal, so a description containing a quote
// emits a block no parser accepts — and nothing validates it.
func TestJSONLDIsValidJSONWithAwkwardText(t *testing.T) {
	d := doc()
	d.Title = `He said "no" — then <script>alert(1)</script>`
	m := published()
	m.Description = `A quote: "it depends", a backslash: \, and a brace: }` + strings.Repeat(" tail", 8)

	head := string(seo.Head(d, m))

	block := extractJSONLD(t, head)
	var decoded map[string]any
	if err := json.Unmarshal([]byte(block), &decoded); err != nil {
		t.Fatalf("the emitted ld+json does not parse: %v\n%s", err, block)
	}
	if decoded["@type"] != "PresentationDigitalDocument" {
		t.Errorf("@type = %v", decoded["@type"])
	}
	if decoded["name"] != d.Title {
		t.Errorf("name round-tripped as %q, want %q", decoded["name"], d.Title)
	}
	// json.Marshal escapes < and > by default, which is what keeps a value
	// from closing the script element early.
	if strings.Contains(block, "</script>") {
		t.Errorf("a value closed the script element:\n%s", block)
	}
}

func TestJSONLDCarriesTheSlideCount(t *testing.T) {
	// numberOfPages is the property a document type carries and a WebPage does
	// not; it is why PresentationDigitalDocument is the right @type.
	block := extractJSONLD(t, string(seo.Head(doc(), published())))

	var decoded struct {
		Pages int `json:"numberOfPages"`
	}
	if err := json.Unmarshal([]byte(block), &decoded); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if decoded.Pages != 7 {
		t.Errorf("numberOfPages = %d, want 7", decoded.Pages)
	}
}

func TestJSONLDTypesImageCorrectly(t *testing.T) {
	// schema.org types thumbnailUrl as URL and image as ImageObject-or-URL.
	// An ImageObject under thumbnailUrl is a type error no validator in this
	// fleet would catch.
	block := extractJSONLD(t, string(seo.Head(doc(), published())))

	var decoded struct {
		Thumbnail any `json:"thumbnailUrl"`
		Image     struct {
			Type string `json:"@type"`
			URL  string `json:"url"`
		} `json:"image"`
	}
	if err := json.Unmarshal([]byte(block), &decoded); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, isString := decoded.Thumbnail.(string); !isString {
		t.Errorf("thumbnailUrl is %T, want a plain URL string", decoded.Thumbnail)
	}
	if decoded.Image.Type != "ImageObject" || decoded.Image.URL == "" {
		t.Errorf("image = %+v, want a populated ImageObject", decoded.Image)
	}
}

func extractJSONLD(t *testing.T, head string) string {
	t.Helper()
	const open, closeTag = `<script type="application/ld+json">`, "</script>"
	start := strings.Index(head, open)
	if start < 0 {
		t.Fatalf("no ld+json block in:\n%s", head)
	}
	rest := head[start+len(open):]
	end := strings.Index(rest, closeTag)
	if end < 0 {
		t.Fatalf("ld+json block is never closed in:\n%s", head)
	}
	return rest[:end]
}

func TestSitemapListsTheCanonicalURL(t *testing.T) {
	body, err := seo.Sitemap(published())
	if err != nil {
		t.Fatalf("Sitemap: %v", err)
	}

	var decoded struct {
		URLs []struct {
			Loc     string `xml:"loc"`
			Lastmod string `xml:"lastmod"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("the emitted sitemap does not parse: %v\n%s", err, body)
	}
	if len(decoded.URLs) != 1 || decoded.URLs[0].Loc != "https://example.com/decks/q3/" {
		t.Errorf("sitemap urls = %+v", decoded.URLs)
	}
	if !strings.HasPrefix(string(body), xml.Header) {
		t.Error("sitemap is missing the XML declaration")
	}
}

func TestSitemapIsOmittedForANoindexDeck(t *testing.T) {
	// A sitemap listing a page that says noindex is a contradiction a crawler
	// reports rather than resolves.
	m := published()
	m.Robots = "noindex, follow"

	body, err := seo.Sitemap(m)
	if err != nil {
		t.Fatalf("Sitemap: %v", err)
	}
	if body != nil {
		t.Errorf("a noindex deck produced a sitemap:\n%s", body)
	}
}

func TestRobotsTxtAgreesWithTheMetaPolicy(t *testing.T) {
	cases := []struct {
		name       string
		robots     string
		wantRule   string
		unwantRule string
	}{
		{"indexable", seo.RobotsPublic, "Allow: /", "Disallow: /"},
		{"private", seo.RobotsPrivate, "Disallow: /", "Allow: /"},
		{"default is private", "", "Disallow: /", "Allow: /"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := published()
			m.Robots = tc.robots

			body := string(seo.Robots(m))

			if !strings.Contains(body, tc.wantRule) {
				t.Errorf("robots.txt is missing %q:\n%s", tc.wantRule, body)
			}
			if strings.Contains(body, tc.unwantRule) {
				t.Errorf("robots.txt contradicts the meta policy with %q:\n%s", tc.unwantRule, body)
			}
			// Inert while crawling is blocked, correct the moment it is not.
			if !strings.Contains(body, "Sitemap: https://example.com/sitemap.xml") {
				t.Errorf("robots.txt omits the Sitemap directive:\n%s", body)
			}
		})
	}
}

func TestWriteEmitsBothDiscoveryFiles(t *testing.T) {
	out := t.TempDir()

	written, err := seo.Write(out, published())
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(written) != 2 {
		t.Fatalf("Write returned %v, want sitemap.xml and robots.txt", written)
	}
	for _, name := range []string{"sitemap.xml", "robots.txt"} {
		info, err := os.Stat(filepath.Join(out, name))
		if err != nil || info.Size() == 0 {
			t.Errorf("%s was not written: %v", name, err)
		}
	}
}

func TestWriteFailsLoudlyWhenTheOutputDirIsMissing(t *testing.T) {
	if _, err := seo.Write(filepath.Join(t.TempDir(), "absent"), published()); err == nil {
		t.Fatal("want an error writing into a directory that does not exist")
	}
}
