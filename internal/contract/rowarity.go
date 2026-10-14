package contract

import (
	"fmt"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
)

// fieldColumns and fieldRows name the two front-matter keys row-arity
// reasons about. They are not contract vocabulary — any layout that happens
// to set both is checked — so they live here rather than in contract.yaml.
const (
	fieldColumns = "columns"
	fieldRows    = "rows"
)

// RuleRowArity is the fatal rule for a `rows` list whose row lengths
// disagree with `columns`.
//
// The motivating bug: a decimal written with a comma (Portuguese convention,
// "9,8 milhões") inside an unquoted value splits a YAML flow sequence into
// extra cells — `[A, 9,8 x, B]` parses as five items, not three — so every
// value downstream of it lands under the wrong header. The rendered table
// still fits the slide and reads as plausible numbers, so `check`'s
// measurements (overflow, contrast, tiny-type…) all pass: a table of
// nonsense that happens to fit the page looks clean to every rule but this
// one.
const RuleRowArity = "row-arity"

// rowArityFindings reports one finding per row in s.Fields["rows"] whose
// length does not match len(columns) — but only when a row is itself a
// list. A layout can shape rows as maps instead (e.g. one row per
// {name, price, benefits}), and arity is a property of a positional row,
// not a keyed one, so a mapped row must never fire this.
func rowArityFindings(s deck.Slide) []Finding {
	columns, ok := s.Fields[fieldColumns].([]any)
	if !ok {
		return nil
	}
	rows, ok := s.Fields[fieldRows].([]any)
	if !ok {
		return nil
	}

	var findings []Finding
	for i, raw := range rows {
		row, ok := raw.([]any)
		if !ok {
			continue // a mapped row; arity does not apply
		}
		if len(row) == len(columns) {
			continue
		}
		findings = append(findings, Finding{
			Slide: s.Number, Source: s.Source, Rule: RuleRowArity, Key: fieldRows, Fatal: true,
			Detail: rowArityDetail(i+1, len(row), len(columns)),
		})
	}
	return findings
}

// rowArityDetail names the likely cause only when a row has more cells than
// declared: that is overwhelmingly an unquoted value whose comma split it
// into extra cells. Fewer cells has a different, less guessable cause (a
// truncated row, a missing trailing cell), so nothing is guessed there.
func rowArityDetail(rowNum, got, want int) string {
	detail := fmt.Sprintf("row %d has %d %s but %q declares %d", rowNum, got, cellWord(got), fieldColumns, want)
	if got > want {
		detail += " — an unquoted value containing a comma splits into extra cells; quote the cells"
	}
	return detail
}

func cellWord(n int) string {
	if n == 1 {
		return "cell"
	}
	return "cells"
}
