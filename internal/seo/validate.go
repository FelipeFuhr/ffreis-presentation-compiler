package seo

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Finding is one SEO defect. Shaped like check.Finding so the two reports read
// alike.
type Finding struct {
	Field  string `json:"field"`
	Detail string `json:"detail"`
}

// The deck.yaml key paths a finding can name. Spelled once so a renamed key
// cannot be renamed in only some of the messages that point at it.
const (
	fieldTitle        = "title"
	fieldRobots       = "seo.robots"
	fieldDescription  = "seo.description"
	fieldBaseURL      = "seo.base_url"
	fieldOrganization = "seo.organization"
	fieldImage        = "seo.image"
	fieldImageAlt     = "seo.image_alt"
)

// Validate reports what would ship wrong.
//
// Only a published deck is held to the full contract. A private deck has no
// address, no audience and nothing to get wrong beyond its title, so demanding
// a 50-character description of it would be noise that teaches authors to
// ignore the report.
//
// The length bounds are the part no tool in this fleet checks today, which is
// how site descriptions drifted past 300 characters without anyone noticing:
// the fleet's own validator asserts presence only, and html-validate's
// `long-title` rule is explicitly switched off.
func Validate(d Document, m Meta) []Finding {
	var findings []Finding

	title := strings.TrimSpace(d.Title)
	if title == "" {
		findings = append(findings, Finding{fieldTitle, "the deck has no title; it becomes the <title>, og:title and schema name"})
	} else if n := utf8.RuneCountInString(title); n > MaxTitleChars {
		findings = append(findings, Finding{fieldTitle,
			fmt.Sprintf("%d characters; search results truncate past %d", n, MaxTitleChars)})
	}

	if n := utf8.RuneCountInString(m.robots()); n > MaxRobotsChars {
		findings = append(findings, Finding{fieldRobots,
			fmt.Sprintf("%d characters; this is a directive list, not prose", n)})
	}

	if !m.Published() {
		return findings
	}
	return append(findings, publishedFindings(d, m)...)
}

// publishedFindings holds the contract that applies only once a deck declares
// a public address.
func publishedFindings(d Document, m Meta) []Finding {
	var findings []Finding

	findings = append(findings, descriptionFindings(d, m)...)

	if !strings.HasPrefix(m.BaseURL, "https://") {
		findings = append(findings, Finding{fieldBaseURL,
			fmt.Sprintf("%q is not an absolute https URL; a relative canonical, og:url or sitemap entry is ignored", m.BaseURL)})
	}
	if strings.TrimSpace(m.Organization) == "" {
		findings = append(findings, Finding{fieldOrganization,
			"a published deck has no author, og:site_name or schema publisher without it"})
	}
	// An unfurler shows the image or it shows nothing; there is no fallback to
	// pick from, so an absent one is a finding rather than a silent degrade.
	if m.imageURL() == "" {
		findings = append(findings, Finding{fieldImage,
			"no social image; a shared link unfurls as text with no preview"})
	} else if strings.TrimSpace(m.ImageAlt) == "" {
		findings = append(findings, Finding{fieldImageAlt,
			"a social image with no alt text is unreadable to anyone using a screen reader"})
	}
	return findings
}

func descriptionFindings(d Document, m Meta) []Finding {
	desc := m.description(d)
	if desc == "" {
		return []Finding{{fieldDescription,
			"no description and no deck subtitle to fall back on; search results and link previews would show an arbitrary excerpt"}}
	}

	n := utf8.RuneCountInString(desc)
	switch {
	case n < MinDescChars:
		return []Finding{{fieldDescription,
			fmt.Sprintf("%d characters; under %d reads as a fragment in a search result", n, MinDescChars)}}
	case n > MaxDescChars:
		return []Finding{{fieldDescription,
			fmt.Sprintf("%d characters; truncated past %d", n, MaxDescChars)}}
	}
	return nil
}

// Report renders findings for a terminal.
func Report(findings []Finding) string {
	if len(findings) == 0 {
		return "seo: no findings\n"
	}
	var b strings.Builder
	for _, f := range findings {
		fmt.Fprintf(&b, "%-18s %s\n", f.Field, f.Detail)
	}
	fmt.Fprintf(&b, "\nseo: %d finding(s)\n", len(findings))
	return b.String()
}
