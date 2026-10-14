package contract_test

import (
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/contract"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
)

// tableSlide builds a "table"-layout slide (described by the shipped default
// contract.yaml, which requires columns and rows) with the given fields, so
// each case below only has to state what differs.
func tableSlide(fields map[string]any) *deck.Deck {
	return &deck.Deck{Slides: []deck.Slide{
		{Number: 1, Source: "slides/01-table.md", Layout: "table", Fields: fields},
	}}
}

// TestValidateReportsARowWithTooManyCells is the motivating bug: a decimal
// comma inside an unquoted value splits a YAML flow sequence into extra
// cells, so the row has more entries than the header declares.
func TestValidateReportsARowWithTooManyCells(t *testing.T) {
	th := loadSampleTheme(t)
	d := tableSlide(map[string]any{
		"columns": []any{"Site", "Visits", "Members", "Ratio", "Revenue"},
		"rows": []any{
			[]any{"Site A", "9", "8 thousand", "103", "4 thousand", "1", "2 percent", "90 thousand"},
		},
	})

	findings := contract.Validate(d, th, t.TempDir())
	if len(findings) != 1 {
		t.Fatalf("findings = %+v, want exactly 1", findings)
	}
	f := findings[0]
	if f.Rule != contract.RuleRowArity || !f.Fatal {
		t.Fatalf("finding = %+v, want a fatal row-arity finding", f)
	}
	for _, want := range []string{"row 1", "8 cells", `"columns" declares 5`, "quote the cells"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("detail %q does not mention %q", f.Detail, want)
		}
	}
}

// TestValidateReportsARowWithTooFewCellsWithoutGuessingACause covers the
// other direction: fewer cells than declared is still an arity mismatch, but
// the comma-splitting explanation does not apply, so it must not be guessed.
func TestValidateReportsARowWithTooFewCellsWithoutGuessingACause(t *testing.T) {
	th := loadSampleTheme(t)
	d := tableSlide(map[string]any{
		"columns": []any{"Site", "Visits", "Members", "Ratio", "Revenue"},
		"rows": []any{
			[]any{"Site A", "12,400"},
		},
	})

	findings := contract.Validate(d, th, t.TempDir())
	if len(findings) != 1 || findings[0].Rule != contract.RuleRowArity {
		t.Fatalf("findings = %+v, want exactly 1 row-arity finding", findings)
	}
	detail := findings[0].Detail
	if !strings.Contains(detail, "row 1 has 2 cells") || !strings.Contains(detail, `"columns" declares 5`) {
		t.Errorf("detail %q does not name both counts", detail)
	}
	if strings.Contains(detail, "comma") {
		t.Errorf("detail %q guesses the comma-splitting cause for too few cells, which is a different failure mode", detail)
	}
}

// TestValidateAcceptsAMatchingRow is the control: a row whose length equals
// len(columns) must not fire, including when a cell's own value contains a
// comma as long as it is quoted (the sample deck's own table does exactly
// this for currency and percentage values).
func TestValidateAcceptsAMatchingRow(t *testing.T) {
	th := loadSampleTheme(t)
	d := tableSlide(map[string]any{
		"columns": []any{"Site", "Units / week", "Unit cost"},
		"rows": []any{
			[]any{"Site A", "12,400", "R$ 8.10"},
		},
	})

	findings := contract.Validate(d, th, t.TempDir())
	if len(findings) != 0 {
		t.Errorf("findings = %s, want none", contract.Report(findings))
	}
}

// TestValidateIgnoresMappedRowsShapedLikeAPricingTable guards the one case
// the rule must never flag: a layout whose rows are a list of maps (one row
// per {name, price, benefits}, say), not a list of lists. Arity is a
// property of a positional row; a keyed row has no "cell count" to compare
// against columns at all, so nobody should ever "simplify" this guard away.
func TestValidateIgnoresMappedRowsShapedLikeAPricingTable(t *testing.T) {
	th := loadSampleTheme(t)
	d := tableSlide(map[string]any{
		"columns": []any{"Name", "Price", "Benefits"},
		"rows": []any{
			map[string]any{"name": "Basic", "price": "9", "benefits": "Email support"},
			map[string]any{"name": "Pro", "price": "29", "benefits": "Priority support, SSO"},
		},
	})

	findings := contract.Validate(d, th, t.TempDir())
	for _, f := range findings {
		if f.Rule == contract.RuleRowArity {
			t.Errorf("row-arity fired on a mapped (plans-table-shaped) row: %+v", f)
		}
	}
}

// TestValidateSkipsRowArityWhenColumnsOrRowsAreNotLists covers the defensive
// branch: neither field being a list must not panic and must not report
// anything — row-arity has nothing to compare in that case, and some other
// rule (missing-field, unknown-field) is responsible for flagging it instead.
func TestValidateSkipsRowArityWhenColumnsOrRowsAreNotLists(t *testing.T) {
	th := loadSampleTheme(t)

	t.Run("rows is not a list", func(t *testing.T) {
		d := tableSlide(map[string]any{
			"columns": []any{"Site", "Visits"},
			"rows":    "not a list",
		})
		findings := contract.Validate(d, th, t.TempDir())
		for _, f := range findings {
			if f.Rule == contract.RuleRowArity {
				t.Errorf("row-arity fired with a non-list rows field: %+v", f)
			}
		}
	})

	t.Run("columns is not a list", func(t *testing.T) {
		d := tableSlide(map[string]any{
			"columns": "not a list",
			"rows":    []any{[]any{"Site A", "12,400"}},
		})
		findings := contract.Validate(d, th, t.TempDir())
		for _, f := range findings {
			if f.Rule == contract.RuleRowArity {
				t.Errorf("row-arity fired with a non-list columns field: %+v", f)
			}
		}
	})

	t.Run("columns is absent entirely", func(t *testing.T) {
		d := tableSlide(map[string]any{
			"rows": []any{[]any{"Site A", "12,400"}},
		})
		findings := contract.Validate(d, th, t.TempDir())
		for _, f := range findings {
			if f.Rule == contract.RuleRowArity {
				t.Errorf("row-arity fired with no columns field at all: %+v", f)
			}
		}
	})
}

// TestValidateReportsOneFindingPerBadRow is the point of walking every row
// instead of stopping at the first: seven damaged rows must be seven
// findings, the same "report everything in one pass" contract the rest of
// this package already holds to.
func TestValidateReportsOneFindingPerBadRow(t *testing.T) {
	th := loadSampleTheme(t)
	d := tableSlide(map[string]any{
		"columns": []any{"Site", "Visits", "Members"},
		"rows": []any{
			[]any{"Site A", "1", "2", "extra"},
			[]any{"Site B", "1", "2"},
			[]any{"Site C", "1", "2", "extra", "extra2"},
		},
	})

	findings := contract.Validate(d, th, t.TempDir())
	count := 0
	for _, f := range findings {
		if f.Rule == contract.RuleRowArity {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("row-arity findings = %d, want 2 (rows 1 and 3; row 2 matches)", count)
	}
}
