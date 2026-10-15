package contract

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/gen"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/pathsafe"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/theme"
)

// Rule names. They are stable strings — printed in Report and grepped by
// `make validate-selftest` — so a rule firing on the broken fixture proves
// the validator still catches it, not just that something failed.
const (
	RuleInvalidContract = "invalid-contract"
	RuleUnknownLayout   = "unknown-layout"
	RuleMissingField    = "missing-field"
	RuleUnknownField    = "unknown-field"
	RuleInvalidField    = "invalid-field"
	RuleMissingAsset    = "missing-asset"
	RuleUnsafePath      = "unsafe-path"
	// RuleUndescribedLayout is the one advisory rule: the theme provides the
	// layout, so the deck is renderable, but no contract describes its fields
	// so they went unchecked.
	RuleUndescribedLayout = "undescribed-layout"
)

// Finding is one contract violation.
type Finding struct {
	// Slide is the 1-based slide number; 0 for a deck-level finding.
	Slide int
	// Source is the slide's front-matter file; empty for a deck-level finding.
	Source string
	Rule   string
	Key    string
	Detail string
	// Fatal distinguishes "this deck is wrong" from "this deck was not fully
	// checked". Only the former may fail a build: a theme is allowed to ship a
	// layout with nothing but a template — that is the whole point of the
	// Fields design — so holding it to a contract it never wrote would make
	// every existing theme un-buildable on upgrade.
	Fatal bool
}

// Fatal reports whether any finding should fail a build. Advisory findings are
// still printed; silently dropping them would turn the gate vacuous for
// exactly the themes that most need describing.
func Fatal(findings []Finding) bool {
	for _, f := range findings {
		if f.Fatal {
			return true
		}
	}
	return false
}

// Validate checks every slide of d against th's field contract and the
// deck's own assets, naming the slide's source file and the offending key
// for each problem. It never stops at the first one: a deck with eight bad
// keys returns eight findings.
//
// A deck's theme: value is not re-checked here — theme.Load already resolves
// it through pathsafe.Contain before th exists, so by the time Validate runs
// the theme name has already proven safe.
func Validate(d *deck.Deck, th *theme.Theme, deckRoot string) []Finding {
	c, err := Load(th.Dir)
	if err != nil {
		return []Finding{{Rule: RuleInvalidContract, Detail: err.Error(), Fatal: true}}
	}

	provided := make(map[string]bool, len(th.LayoutNames()))
	for _, name := range th.LayoutNames() {
		provided[name] = true
	}

	var findings []Finding
	undescribed := map[string]bool{}
	for _, s := range d.Slides {
		findings = append(findings, validateSlide(s, c, th, provided, deckRoot)...)
		if provided[s.Layout] {
			if _, described := c.Layouts[s.Layout]; !described {
				undescribed[s.Layout] = true
			}
		}
	}
	return append(findings, undescribedFindings(th, undescribed)...)
}

// undescribedFindings reports once per layout, not once per slide: a theme
// with ten undescribed layouts across thirty slides should produce ten lines,
// not thirty.
func undescribedFindings(th *theme.Theme, undescribed map[string]bool) []Finding {
	names := make([]string, 0, len(undescribed))
	for name := range undescribed {
		names = append(names, name)
	}
	sort.Strings(names)

	findings := make([]Finding, 0, len(names))
	for _, name := range names {
		findings = append(findings, Finding{
			Rule: RuleUndescribedLayout, Key: name,
			Detail: fmt.Sprintf("theme %q provides layout %q but no contract describes its fields, "+
				"so they were not checked; add it to %s/contract.yaml", th.Name, name, th.Dir),
		})
	}
	return findings
}

// validateSlide checks one slide's layout and fields.
//
// Whether the layout exists is the *theme's* answer, not the contract's. These
// were conflated once, and the symptom was an error that read `theme "x" has
// no layout "table" (available: … table …)` — the decision came from the
// contract map while the message came from the theme. A theme is allowed to
// provide layouts no contract describes; what that costs is field checking,
// not the build.
//
// An unknown layout makes every other field check meaningless, so it is
// reported alone.
func validateSlide(s deck.Slide, c Contract, th *theme.Theme, provided map[string]bool, deckRoot string) []Finding {
	if !provided[s.Layout] {
		return []Finding{{
			Slide: s.Number, Source: s.Source, Rule: RuleUnknownLayout, Key: s.Layout, Fatal: true,
			Detail: fmt.Sprintf("theme %q has no layout %q (available: %s)",
				th.Name, s.Layout, strings.Join(th.LayoutNames(), ", ")),
		}}
	}
	layout, described := c.Layouts[s.Layout]
	if !described {
		return nil // reported once per layout by undescribedFindings
	}

	var findings []Finding
	findings = append(findings, missingFields(s, layout)...)
	findings = append(findings, fieldFindings(s, layout, deckRoot)...)
	findings = append(findings, rowArityFindings(s)...)
	return findings
}

// missingFields reports each field the layout requires but the slide does
// not set.
//
// A chart/diagram block satisfies `media`. The generator writes the image and
// sets that field, but it runs AFTER this stage — so requiring the key here
// would reject every generated slide for not yet having what the next step
// produces. The declaration the author actually made is the chart block.
func missingFields(s deck.Slide, layout Layout) []Finding {
	generated := hasSpec(s)
	var findings []Finding
	for _, name := range layout.Required {
		if name == gen.MediaKey && generated {
			continue
		}
		if _, ok := s.Fields[name]; !ok {
			findings = append(findings, Finding{
				Slide: s.Number, Source: s.Source, Rule: RuleMissingField, Key: name, Fatal: true,
				Detail: fmt.Sprintf("layout %q requires %q", s.Layout, name),
			})
		}
	}
	return findings
}

// fieldFindings walks every field a slide sets. A chart/diagram block is
// recognised globally — on any layout, the same way chrome keys are — and
// validated once against its own sub-contract; `media` is checked as an
// asset; anything else must be declared by the layout.
func fieldFindings(s deck.Slide, layout Layout, deckRoot string) []Finding {
	known := layout.known()
	findings := specFindings(s, deckRoot)
	for _, key := range sortedKeys(s.Fields) {
		switch {
		case isSpecKey(key):
			continue // already validated once, above, regardless of which alias is used
		case key == gen.MediaKey:
			findings = append(findings, assetFinding(s, key, s.Fields[key], deckRoot)...)
		case !known[key]:
			findings = append(findings, Finding{
				Slide: s.Number, Source: s.Source, Rule: RuleUnknownField, Key: key, Fatal: true,
				Detail: fmt.Sprintf("layout %q does not declare %q", s.Layout, key),
			})
		}
	}
	return findings
}

// hasSpec reports whether the slide declares a chart/diagram block.
func hasSpec(s deck.Slide) bool {
	for _, k := range gen.SpecKeys {
		if _, ok := s.Fields[k]; ok {
			return true
		}
	}
	return false
}

func isSpecKey(key string) bool {
	for _, k := range gen.SpecKeys {
		if k == key {
			return true
		}
	}
	return false
}

// specFindings checks a chart/diagram block's own spec/data paths. It reuses
// gen.SpecOf so this package and the generator hook agree on what a
// well-formed block looks like, instead of a second parser that could drift.
func specFindings(s deck.Slide, deckRoot string) []Finding {
	spec, key, err := gen.SpecOf(&s)
	if err != nil {
		return []Finding{{Slide: s.Number, Source: s.Source, Rule: RuleInvalidField, Key: key, Detail: err.Error(), Fatal: true}}
	}
	if spec == nil {
		return nil
	}
	findings := assetFinding(s, key+".spec", spec.Spec, deckRoot)
	if spec.Data != "" {
		findings = append(findings, assetFinding(s, key+".data", spec.Data, deckRoot)...)
	}
	return findings
}

// assetFinding checks that raw is a non-empty string path that resolves
// inside deckRoot and exists on disk, in that order: a path that escapes the
// root is a security finding regardless of whether anything lives there.
func assetFinding(s deck.Slide, key string, raw any, deckRoot string) []Finding {
	path, ok := raw.(string)
	if !ok || path == "" {
		return []Finding{{
			Slide: s.Number, Source: s.Source, Rule: RuleInvalidField, Key: key, Fatal: true,
			Detail: fmt.Sprintf("%q must be a non-empty string path", key),
		}}
	}
	abs, err := pathsafe.Contain(deckRoot, path)
	if err != nil {
		return []Finding{{Slide: s.Number, Source: s.Source, Rule: RuleUnsafePath, Key: key, Detail: err.Error(), Fatal: true}}
	}
	if _, err := os.Stat(abs); err != nil {
		return []Finding{{
			Slide: s.Number, Source: s.Source, Rule: RuleMissingAsset, Key: key, Fatal: true,
			Detail: fmt.Sprintf("%s %q does not exist", key, path),
		}}
	}
	return nil
}

func sortedKeys(fields map[string]any) []string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Report renders findings as a human-readable summary, one line per finding,
// mirroring internal/check's Finding/Report shape so the two read alike.
func Report(findings []Finding) string {
	if len(findings) == 0 {
		return "contract: no findings\n"
	}
	var b strings.Builder
	advisory := 0
	for _, f := range findings {
		where := "deck"
		if f.Slide > 0 {
			where = fmt.Sprintf("slide %d (%s)", f.Slide, f.Source)
		}
		note := ""
		if !f.Fatal {
			note = " [advisory]"
			advisory++
		}
		fmt.Fprintf(&b, "%s: %s%s: %s\n", where, f.Rule, note, f.Detail)
	}
	fmt.Fprintf(&b, "\ncontract: %d finding(s) across %d rule(s)",
		len(findings), len(Rules(findings)))
	if advisory > 0 {
		fmt.Fprintf(&b, "; %d advisory, which do not fail the build", advisory)
	}
	b.WriteString("\n")
	return b.String()
}

// Rules lists the distinct rules present in findings, sorted.
func Rules(findings []Finding) []string {
	seen := map[string]bool{}
	for _, f := range findings {
		seen[f.Rule] = true
	}
	rules := make([]string, 0, len(seen))
	for r := range seen {
		rules = append(rules, r)
	}
	sort.Strings(rules)
	return rules
}
