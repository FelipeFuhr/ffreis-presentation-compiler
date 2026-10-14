package check_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/check"
)

// fakePDF builds just enough of a PDF byte stream to exercise the page-object
// scan: a header, a page-tree node (the false-positive trap), and pageCount
// leaf page objects, half written with a space after /Type and half without.
//
// The header is written as two concatenated literals, not one: a real PDF
// signature followed by a version and newline is exactly what libmagic (and
// so lefthook's binary-file hygiene check, which shells out to `file`) sniffs
// a PDF by, and this fixture's own Go source would otherwise trip it.
func fakePDF(pageCount int) []byte {
	var b strings.Builder
	b.WriteString("%PDF" + "-1.7\n")
	b.WriteString("1 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\n")
	for i := 0; i < pageCount; i++ {
		if i%2 == 0 {
			b.WriteString("2 0 obj\n<< /Type /Page /Parent 1 0 R >>\nendobj\n")
		} else {
			b.WriteString("2 0 obj\n<< /Type/Page /Parent 1 0 R >>\nendobj\n")
		}
	}
	return []byte(b.String())
}

func TestCountPDFPages(t *testing.T) {
	t.Run("counts page objects with and without the space", func(t *testing.T) {
		got, err := check.CountPDFPages(fakePDF(5))
		if err != nil {
			t.Fatalf("CountPDFPages: %v", err)
		}
		if got != 5 {
			t.Errorf("got %d pages, want 5", got)
		}
	})

	t.Run("does not count the Pages tree node as a page", func(t *testing.T) {
		// fakePDF(0) has exactly one "/Type /Pages" node and no "/Type /Page".
		got, err := check.CountPDFPages(fakePDF(0))
		if err != nil {
			t.Fatalf("CountPDFPages: %v", err)
		}
		if got != 0 {
			t.Errorf("got %d pages, want 0 — \"/Type /Pages\" must not match \"/Type /Page\"", got)
		}
	})

	t.Run("rejects a file with no PDF header", func(t *testing.T) {
		_, err := check.CountPDFPages([]byte("not a pdf at all"))
		if err == nil {
			t.Fatal("want an error for a malformed file")
		}
	})

	t.Run("rejects an empty file", func(t *testing.T) {
		_, err := check.CountPDFPages(nil)
		if err == nil {
			t.Fatal("want an error for an empty file")
		}
	})
}

func TestCountSlideSections(t *testing.T) {
	html := []byte(`<section data-slide="1">a</section><section data-slide="2">b</section>`)
	if got := check.CountSlideSections(html); got != 2 {
		t.Errorf("got %d, want 2", got)
	}
	if got := check.CountSlideSections([]byte("<p>no slides here</p>")); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func TestComparePDFPages(t *testing.T) {
	t.Run("matching counts produce no finding", func(t *testing.T) {
		path := writeFixture(t, fakePDF(3))
		finding, err := check.ComparePDFPages(path, 3)
		if err != nil {
			t.Fatalf("ComparePDFPages: %v", err)
		}
		if finding != nil {
			t.Errorf("want no finding when counts match, got %+v", finding)
		}
	})

	t.Run("a mismatch is reported as a deck-level finding", func(t *testing.T) {
		path := writeFixture(t, fakePDF(3))
		finding, err := check.ComparePDFPages(path, 5)
		if err != nil {
			t.Fatalf("ComparePDFPages: %v", err)
		}
		if finding == nil {
			t.Fatal("want a finding when counts mismatch")
		}
		if finding.Slide != 0 {
			t.Errorf("Slide = %d, want 0 (deck-level, like font-fallback)", finding.Slide)
		}
		if finding.Rule != check.RulePDFPages {
			t.Errorf("Rule = %q, want %q", finding.Rule, check.RulePDFPages)
		}
		if !strings.Contains(finding.Detail, "3 page") || !strings.Contains(finding.Detail, "5 slide") {
			t.Errorf("Detail does not name both counts: %q", finding.Detail)
		}
	})

	t.Run("a missing pdf is an error, not a silent pass", func(t *testing.T) {
		_, err := check.ComparePDFPages(filepath.Join(t.TempDir(), "absent.pdf"), 1)
		if err == nil {
			t.Fatal("want an error when the pdf cannot be read")
		}
	})

	t.Run("a malformed pdf surfaces its own error", func(t *testing.T) {
		path := writeFixture(t, []byte("garbage"))
		_, err := check.ComparePDFPages(path, 1)
		if err == nil {
			t.Fatal("want an error for a malformed pdf")
		}
	})
}

func writeFixture(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.pdf")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}
