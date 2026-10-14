package seo_test

import (
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/seo"
)

// fields returns the Field of every finding, so a test asserts on which rules
// fired rather than on wording.
func fields(findings []seo.Finding) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.Field)
	}
	return out
}

func contains(have []string, want string) bool {
	for _, h := range have {
		if h == want {
			return true
		}
	}
	return false
}

func TestValidateIsSilentForAWellFormedPublishedDeck(t *testing.T) {
	if findings := seo.Validate(doc(), published()); len(findings) != 0 {
		t.Errorf("want no findings, got %v", findings)
	}
}

func TestValidateHoldsAPrivateDeckToTheTitleOnly(t *testing.T) {
	// Demanding a 50-character description of an internal document would be
	// noise that teaches authors to ignore the report.
	findings := seo.Validate(doc(), seo.Meta{})

	if len(findings) != 0 {
		t.Errorf("a private deck with a good title produced %v", findings)
	}
}

func TestValidateReportsAPrivateDecksOverlongTitle(t *testing.T) {
	d := doc()
	d.Title = strings.Repeat("a", seo.MaxTitleChars+1)

	got := fields(seo.Validate(d, seo.Meta{}))

	if !contains(got, "title") {
		t.Errorf("an overlong title was not reported even privately: %v", got)
	}
}

func TestValidateCountsRunesNotBytes(t *testing.T) {
	// A Portuguese or Spanish title is shorter than its byte length. Counting
	// bytes would report a 40-character title as overlong.
	d := doc()
	d.Title = strings.Repeat("ã", seo.MaxTitleChars)

	if findings := seo.Validate(d, seo.Meta{}); len(findings) != 0 {
		t.Errorf("a %d-rune title was reported as overlong: %v", seo.MaxTitleChars, findings)
	}
}

func TestValidatePublishedDeckRules(t *testing.T) {
	cases := []struct {
		name  string
		mutit func(*seo.Meta)
		want  string
	}{
		{
			name:  "description absent and no subtitle to borrow",
			mutit: func(m *seo.Meta) { m.Description = "" },
			want:  "seo.description",
		},
		{
			name:  "description too short to read as a result",
			mutit: func(m *seo.Meta) { m.Description = "Too short." },
			want:  "seo.description",
		},
		{
			name:  "description truncated in results",
			mutit: func(m *seo.Meta) { m.Description = strings.Repeat("x", seo.MaxDescChars+1) },
			want:  "seo.description",
		},
		{
			name:  "base_url is not absolute https",
			mutit: func(m *seo.Meta) { m.BaseURL = "example.com" },
			want:  "seo.base_url",
		},
		{
			name:  "no organization for author or publisher",
			mutit: func(m *seo.Meta) { m.Organization = "" },
			want:  "seo.organization",
		},
		{
			name:  "no social image",
			mutit: func(m *seo.Meta) { m.Image = "" },
			want:  "seo.image",
		},
		{
			name:  "social image with no alt text",
			mutit: func(m *seo.Meta) { m.ImageAlt = "" },
			want:  "seo.image_alt",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := published()
			tc.mutit(&m)
			// The subtitle would otherwise satisfy the description rule.
			d := doc()
			d.Subtitle = ""

			got := fields(seo.Validate(d, m))

			if !contains(got, tc.want) {
				t.Errorf("want a %q finding, got %v", tc.want, got)
			}
		})
	}
}

func TestValidateDoesNotDemandAnImageAltWithNoImage(t *testing.T) {
	// Two findings for one missing field would make the report read as twice
	// the work it is.
	m := published()
	m.Image, m.ImageAlt = "", ""

	got := fields(seo.Validate(doc(), m))

	if contains(got, "seo.image_alt") {
		t.Errorf("reported a missing alt for an image that does not exist: %v", got)
	}
}

func TestReportNamesEveryFinding(t *testing.T) {
	m := published()
	m.Organization, m.Image, m.ImageAlt = "", "", ""

	out := seo.Report(seo.Validate(doc(), m))

	for _, want := range []string{"seo.organization", "seo.image", "2 finding"} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing %q:\n%s", want, out)
		}
	}
}

func TestReportSaysSoWhenClean(t *testing.T) {
	if out := seo.Report(nil); !strings.Contains(out, "no findings") {
		t.Errorf("Report(nil) = %q", out)
	}
}
