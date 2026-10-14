package contract_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/contract"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/theme"
)

const (
	sampleDeck   = "../../examples/sample-deck"
	sampleThemes = "../../examples/themes"
)

func loadSampleTheme(t *testing.T) *theme.Theme {
	t.Helper()
	th, err := theme.Load(sampleThemes, "sample")
	if err != nil {
		t.Fatalf("theme.Load: %v", err)
	}
	return th
}

func TestValidateAcceptsTheShippedSampleDeck(t *testing.T) {
	d, err := deck.Load(sampleDeck)
	if err != nil {
		t.Fatalf("deck.Load: %v", err)
	}
	th := loadSampleTheme(t)

	findings := contract.Validate(d, th, sampleDeck)
	if len(findings) != 0 {
		t.Errorf("the shipped example deck must satisfy the shipped default contract, got %s", contract.Report(findings))
	}
}

func TestValidateReportsAnUnknownLayout(t *testing.T) {
	th := loadSampleTheme(t)
	d := &deck.Deck{Slides: []deck.Slide{
		{Number: 1, Source: "slides/01-x.md", Layout: "not-a-real-layout"},
	}}

	findings := contract.Validate(d, th, t.TempDir())
	if len(findings) != 1 {
		t.Fatalf("findings = %+v, want exactly 1", findings)
	}
	f := findings[0]
	if f.Rule != contract.RuleUnknownLayout || f.Slide != 1 || f.Source != "slides/01-x.md" {
		t.Errorf("finding = %+v, want unknown-layout on slide 1", f)
	}
	if !strings.Contains(f.Detail, "table") {
		t.Errorf("detail %q does not list an available layout", f.Detail)
	}
}

func TestValidateReportsEveryMissingAndUnknownField(t *testing.T) {
	th := loadSampleTheme(t)
	d := &deck.Deck{Slides: []deck.Slide{
		{Number: 3, Source: "slides/03-table.md", Layout: "table", Fields: map[string]any{
			"mystery": "surprise",
		}},
	}}

	findings := contract.Validate(d, th, t.TempDir())
	rules := contract.Rules(findings)
	if strings.Join(rules, ",") != "missing-field,unknown-field" {
		t.Fatalf("rules = %v, want missing-field and unknown-field", rules)
	}
	// table requires both columns and rows: omitting both is two findings, not
	// one, which is the point of reporting all findings instead of the first.
	missing := 0
	for _, f := range findings {
		if f.Rule == contract.RuleMissingField {
			missing++
		}
	}
	if missing != 2 {
		t.Errorf("missing-field findings = %d, want 2 (columns and rows)", missing)
	}
}

func TestValidateReportsAMissingAsset(t *testing.T) {
	th := loadSampleTheme(t)
	root := t.TempDir()
	d := &deck.Deck{Slides: []deck.Slide{
		{Number: 4, Source: "slides/04-split.md", Layout: "split", Fields: map[string]any{
			"alt": "a chart", "media": "assets/does-not-exist.png",
		}},
	}}

	findings := contract.Validate(d, th, root)
	if len(findings) != 1 {
		t.Fatalf("findings = %+v, want exactly 1", findings)
	}
	f := findings[0]
	if f.Rule != contract.RuleMissingAsset {
		t.Errorf("rule = %q, want missing-asset", f.Rule)
	}
	if !strings.Contains(f.Detail, "media") || !strings.Contains(f.Detail, "does-not-exist.png") {
		t.Errorf("detail %q does not name the field and the path", f.Detail)
	}
}

func TestValidateRejectsAMediaPathThatEscapesTheDeckRoot(t *testing.T) {
	th := loadSampleTheme(t)
	d := &deck.Deck{Slides: []deck.Slide{
		{Number: 4, Source: "slides/04-split.md", Layout: "split", Fields: map[string]any{
			"alt": "a chart", "media": "../../../etc/passwd",
		}},
	}}

	findings := contract.Validate(d, th, t.TempDir())
	if len(findings) != 1 || findings[0].Rule != contract.RuleUnsafePath {
		t.Fatalf("findings = %+v, want exactly 1 unsafe-path finding", findings)
	}
}

func TestValidateChecksAChartBlocksSpecAndDataAssets(t *testing.T) {
	th := loadSampleTheme(t)
	root := t.TempDir()
	d := &deck.Deck{Slides: []deck.Slide{
		{Number: 7, Source: "slides/07-generated.md", Layout: "split", Fields: map[string]any{
			"alt": "a chart",
			"chart": map[string]any{
				"generator": "python", "spec": "charts/absent.py", "data": "data/absent.csv",
			},
		}},
	}}

	findings := contract.Validate(d, th, root)
	rules := contract.Rules(findings)
	if strings.Join(rules, ",") != "missing-asset" || len(findings) != 2 {
		t.Fatalf("findings = %+v, want 2 missing-asset findings (spec and data)", findings)
	}
	var keys []string
	for _, f := range findings {
		keys = append(keys, f.Key)
	}
	if strings.Join(keys, ",") != "chart.spec,chart.data" {
		t.Errorf("keys = %v, want chart.spec and chart.data named", keys)
	}
}

func TestValidateAcceptsAGeneratedSlideWithNoMediaField(t *testing.T) {
	// A chart/diagram block produces media at gen.Apply time, which runs after
	// contract validation; a slide that relies on it must not be flagged for
	// "missing" media it has not generated yet.
	th := loadSampleTheme(t)
	d := &deck.Deck{Slides: []deck.Slide{
		{Number: 7, Source: "slides/07-generated.md", Layout: "split", Fields: map[string]any{
			"alt": "a chart",
			"chart": map[string]any{
				"generator": "python", "spec": "charts/bars.py", "data": "data/throughput.csv",
			},
		}},
	}}

	findings := contract.Validate(d, th, sampleDeck)
	if len(findings) != 0 {
		t.Errorf("findings = %s, want none", contract.Report(findings))
	}
}

func TestValidateReportsAMalformedChartBlock(t *testing.T) {
	th := loadSampleTheme(t)
	d := &deck.Deck{Slides: []deck.Slide{
		{Number: 7, Source: "slides/07-generated.md", Layout: "split", Fields: map[string]any{
			"alt": "a chart", "chart": "not-a-mapping",
		}},
	}}

	findings := contract.Validate(d, th, t.TempDir())
	if len(findings) != 1 || findings[0].Rule != contract.RuleInvalidField {
		t.Fatalf("findings = %+v, want exactly 1 invalid-field finding", findings)
	}
}

func TestValidateReportsAnInvalidContractOverrideAsASingleFinding(t *testing.T) {
	root := writeMinimalTheme(t)
	if err := os.WriteFile(filepath.Join(root, "contract.yaml"), []byte("layouts: [not a map"), 0o600); err != nil {
		t.Fatalf("write override: %v", err)
	}
	th, err := theme.Load(filepath.Dir(root), filepath.Base(root))
	if err != nil {
		t.Fatalf("theme.Load: %v", err)
	}

	d := &deck.Deck{Slides: []deck.Slide{{Number: 1, Source: "x.md", Layout: "cover"}}}
	findings := contract.Validate(d, th, t.TempDir())
	if len(findings) != 1 || findings[0].Rule != contract.RuleInvalidContract {
		t.Fatalf("findings = %+v, want exactly 1 invalid-contract finding", findings)
	}
}

func TestReport(t *testing.T) {
	t.Run("clean deck", func(t *testing.T) {
		if got := contract.Report(nil); !strings.Contains(got, "no findings") {
			t.Errorf("Report(nil) = %q", got)
		}
	})

	t.Run("names the slide, the file and the rule", func(t *testing.T) {
		got := contract.Report([]contract.Finding{
			{Slide: 7, Source: "slides/07-x.md", Rule: contract.RuleMissingAsset, Key: "media",
				Detail: `media "assets/foo.png" does not exist`},
		})
		for _, want := range []string{"slide 7", "slides/07-x.md", "missing-asset", "assets/foo.png", "1 finding(s)"} {
			if !strings.Contains(got, want) {
				t.Errorf("report is missing %q:\n%s", want, got)
			}
		}
	})

	t.Run("deck-level finding has no slide number", func(t *testing.T) {
		got := contract.Report([]contract.Finding{{Rule: contract.RuleInvalidContract, Detail: "boom"}})
		if strings.Contains(got, "slide 0") {
			t.Errorf("deck-level finding rendered as slide 0:\n%s", got)
		}
	})
}

func TestRules(t *testing.T) {
	got := contract.Rules([]contract.Finding{
		{Rule: contract.RuleMissingField}, {Rule: contract.RuleUnknownField}, {Rule: contract.RuleMissingField},
	})
	if strings.Join(got, ",") != "missing-field,unknown-field" {
		t.Errorf("Rules() = %v, want the distinct rules sorted", got)
	}
}

// writeMinimalTheme writes a theme named "broken" that theme.Load accepts —
// one stylesheet, one layout, a chrome template — and returns its root
// (<dir>/broken), so a test can add a contract.yaml beside it.
func writeMinimalTheme(t *testing.T) string {
	t.Helper()
	parent := t.TempDir()
	root := filepath.Join(parent, "broken")
	files := map[string]string{
		"theme.yaml":           "name: broken\n",
		"slide.gohtml":         "<section>{{ .Content }}</section>",
		"css/base.css":         ":root{}",
		"layouts/cover.gohtml": "<div></div>",
	}
	for rel, body := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}
