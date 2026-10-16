package deck

import (
	"encoding/csv"
	"fmt"
	"os"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/pathsafe"
)

// rowsCSVKey is the front-matter key a slide uses to source a table's rows
// from a CSV file instead of an inline YAML sequence.
//
// The motivating bug: Portuguese and Spanish write decimals with a comma
// ("9,8 milhões"), and an unquoted YAML flow sequence splits on commas —
// `[Grêmio, 9,8 milhões, 103,4 mil]` parses as five cells, not three, and
// every value downstream lands under the wrong header. The table still fits
// the slide and reads as plausible numbers, so nothing about its rendered
// geometry looks wrong; only the content is.
//
// encoding/csv does not have this failure mode, and not only because RFC
// 4180 quoting is unambiguous. csv.Reader rejects a row whose field count
// disagrees with the header at parse time, naming the line — the arity bug
// becomes a parse error instead of a silent misalignment. And CSV is rarely
// hand-typed in the first place: it is what a spreadsheet or pandas' to_csv
// already emits with correct quoting, where a YAML flow sequence is exactly
// the format an author types by hand with no quoting discipline at all.
const rowsCSVKey = "rows_csv"

// loadRowsCSV reads path (resolved under root) and returns columns and rows
// in the []any-of-[]any shape deck.Load produces for inline YAML, so a
// layout template and the contract's row-arity rule treat a CSV- and a
// YAML-sourced table identically — neither cares where the data came from.
//
// declaredColumns is the slide's own `columns:`, when it set one. If it did,
// the CSV header must match it exactly: a CSV column renamed without
// updating `columns:` is a silent misalignment the same shape as the arity
// bug this mechanism exists to prevent, so it fails the build instead.
func loadRowsCSV(root, path string, declaredColumns []any) (columns, rows []any, err error) {
	abs, err := pathsafe.Contain(root, path)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", rowsCSVKey, err)
	}
	f, err := os.Open(abs) //nolint:gosec // contained within the operator's own deck root, checked above
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", rowsCSVKey, err)
	}
	defer func() { _ = f.Close() }() // read-only handle; a close error cannot lose data

	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("%s %s: %w", rowsCSVKey, path, err)
	}
	if len(records) == 0 {
		return nil, nil, fmt.Errorf("%s %s: has no header row", rowsCSVKey, path)
	}
	header := records[0]

	if len(declaredColumns) > 0 {
		if err := matchHeader(path, declaredColumns, header); err != nil {
			return nil, nil, err
		}
		columns = declaredColumns
	} else {
		columns = toAnyRow(header)
	}

	rows = make([]any, 0, len(records)-1)
	for _, record := range records[1:] {
		rows = append(rows, toAnyRow(record))
	}
	return columns, rows, nil
}

// matchHeader compares an explicit `columns:` declaration against the CSV's
// own header, cell by cell, so a column renamed in one place and not the
// other fails the build instead of silently misaligning every value in it.
func matchHeader(path string, declared []any, header []string) error {
	if len(declared) != len(header) {
		return fmt.Errorf("%s %s: header has %d column(s), \"columns:\" declares %d",
			rowsCSVKey, path, len(header), len(declared))
	}
	for i, want := range declared {
		if got := header[i]; fmt.Sprint(want) != got {
			return fmt.Errorf("%s %s: header column %d is %q, \"columns:\" declares %q",
				rowsCSVKey, path, i+1, got, want)
		}
	}
	return nil
}

// toAnyRow widens a CSV record to []any, the shape yaml.Unmarshal produces
// for an inline flow sequence — so rows() and str() in internal/theme, and
// the contract's row-arity rule, need no case for where a row came from.
func toAnyRow(fields []string) []any {
	row := make([]any, len(fields))
	for i, f := range fields {
		row[i] = f
	}
	return row
}

// fieldColumns and fieldRows are the front-matter keys a table-shaped layout
// uses, duplicated from internal/contract's own (unexported) constants of
// the same name: deck is the lower layer here and contract already imports
// it, so the reverse import would cycle. Two string literals, not worth a
// third package to share.
const (
	fieldColumns = "columns"
	fieldRows    = "rows"
)

// resolveRowsCSV consumes rows_csv from fields, when present, replacing it
// with rows (and columns, unless the slide declared its own) loaded from the
// named CSV file. Called during Load, before contract validation ever sees
// the slide — so a CSV- and a YAML-authored table are indistinguishable by
// the time anything downstream looks at fields, and rows_csv itself never
// needs a contract.yaml entry any more than the chrome keys do.
func resolveRowsCSV(root string, fields map[string]any) error {
	raw, ok := fields[rowsCSVKey]
	if !ok {
		return nil
	}
	path, ok := raw.(string)
	if !ok {
		return fmt.Errorf("%q must be a string path", rowsCSVKey)
	}
	if _, already := fields[fieldRows]; already {
		return fmt.Errorf("sets both %q and %q — only one may author a table's rows", rowsCSVKey, fieldRows)
	}

	declared, _ := fields[fieldColumns].([]any)
	columns, rows, err := loadRowsCSV(root, path, declared)
	if err != nil {
		return err
	}
	fields[fieldColumns] = columns
	fields[fieldRows] = rows
	delete(fields, rowsCSVKey)
	return nil
}
