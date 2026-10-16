package deck_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
)

// writeCSV writes a CSV file under root at rel, creating parent directories.
func writeCSV(t *testing.T, root, rel, content string) {
	t.Helper()
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func TestLoadSourcesTableRowsFromCSVAndDerivesColumnsFromItsHeader(t *testing.T) {
	root := writeDeck(t, manifest, map[string]string{
		"01-bench.md": "---\nlayout: table\ntitle: Benchmark\nrows_csv: data/bench.csv\n---\n",
	})
	// The exact bug class this exists to prevent: a pt-BR decimal comma. In
	// CSV, unlike a hand-typed YAML flow sequence, the value must already be
	// quoted to parse at all, so the comma inside it can never split a row.
	writeCSV(t, root, "data/bench.csv", "Clube,Torcedores,Arrecadação\nGrêmio,\"9,8 milhões\",\"R$ 90 milhões\"\n")

	d, err := deck.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	columns, ok := d.Slides[0].Fields["columns"].([]any)
	if !ok || len(columns) != 3 || columns[0] != "Clube" || columns[1] != "Torcedores" {
		t.Fatalf("columns = %#v, want [Clube Torcedores Arrecadação] from the CSV header", columns)
	}
	rows, ok := d.Slides[0].Fields["rows"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("rows = %#v, want 1 data row", rows)
	}
	row, ok := rows[0].([]any)
	if !ok || len(row) != 3 {
		t.Fatalf("row = %#v, want 3 cells", rows[0])
	}
	if row[1] != "9,8 milhões" {
		t.Errorf("row[1] = %q, want the decimal-comma value intact as one cell", row[1])
	}
	if _, stillThere := d.Slides[0].Fields["rows_csv"]; stillThere {
		t.Error("rows_csv must be consumed, not left in Fields alongside the resolved rows")
	}
}

func TestLoadAcceptsDeclaredColumnsMatchingTheCSVHeader(t *testing.T) {
	root := writeDeck(t, manifest, map[string]string{
		"01-bench.md": "---\nlayout: table\ntitle: Benchmark\ncolumns: [Clube, Torcedores]\nrows_csv: data/bench.csv\n---\n",
	})
	writeCSV(t, root, "data/bench.csv", "Clube,Torcedores\nGrêmio,\"9,8 milhões\"\n")

	d, err := deck.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	columns := d.Slides[0].Fields["columns"].([]any)
	if columns[0] != "Clube" || columns[1] != "Torcedores" {
		t.Errorf("columns = %#v, want the slide's own declared values preserved", columns)
	}
}

func TestLoadRejectsACSVHeaderThatDisagreesWithDeclaredColumns(t *testing.T) {
	root := writeDeck(t, manifest, map[string]string{
		"01-bench.md": "---\nlayout: table\ntitle: Benchmark\ncolumns: [Clube, Associados]\nrows_csv: data/bench.csv\n---\n",
	})
	writeCSV(t, root, "data/bench.csv", "Clube,Torcedores\nGrêmio,9800000\n")

	_, err := deck.Load(root)
	if err == nil {
		t.Fatal("want an error: declared columns and the CSV header disagree")
	}
	for _, want := range []string{"Torcedores", "Associados"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestLoadRejectsBothRowsAndRowsCSVOnTheSameSlide(t *testing.T) {
	root := writeDeck(t, manifest, map[string]string{
		"01-bench.md": "---\nlayout: table\ntitle: Benchmark\nrows: [[a, b]]\nrows_csv: data/bench.csv\n---\n",
	})
	writeCSV(t, root, "data/bench.csv", "a,b\n1,2\n")

	_, err := deck.Load(root)
	if err == nil || !strings.Contains(err.Error(), "sets both") {
		t.Fatalf("err = %v, want an error naming both rows and rows_csv", err)
	}
}

func TestLoadRejectsARaggedCSVRow(t *testing.T) {
	root := writeDeck(t, manifest, map[string]string{
		"01-bench.md": "---\nlayout: table\ntitle: Benchmark\nrows_csv: data/bench.csv\n---\n",
	})
	// Row 2 has three fields against a two-column header — exactly the arity
	// bug, except here it is a parse error naming the line, not a table that
	// silently renders with every value shifted one column to the right.
	writeCSV(t, root, "data/bench.csv", "Clube,Torcedores\nGrêmio,9800000,extra\n")

	_, err := deck.Load(root)
	if err == nil || !strings.Contains(err.Error(), "bench.csv") {
		t.Fatalf("err = %v, want an error naming bench.csv", err)
	}
}

func TestLoadRejectsARowsCSVPathThatEscapesTheDeckRoot(t *testing.T) {
	root := writeDeck(t, manifest, map[string]string{
		"01-bench.md": "---\nlayout: table\ntitle: Benchmark\nrows_csv: ../../etc/passwd\n---\n",
	})

	_, err := deck.Load(root)
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("err = %v, want an error saying the path escapes the deck root", err)
	}
}
