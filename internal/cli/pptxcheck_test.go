package cli

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/pptxcheck"
)

// stubPPTXRunner replaces the one boundary onto LibreOffice and poppler, so
// these tests state what the reader "saw" and never need either installed.
//
// The fake honours --outdir, because Run finds its PDF by reading that
// directory rather than by trusting the command: a stub that skipped it would
// pass while leaving Run's actual output discovery unexercised.
func stubPPTXRunner(t *testing.T, words string) {
	t.Helper()
	original := pptxRunner
	pptxRunner = func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "soffice":
			for i, a := range args {
				if a == "--outdir" && i+1 < len(args) {
					// Deliberately not a real PDF header. Run finds its
					// output by name and the extractor is stubbed, so these
					// bytes are never read -- and spelling the real magic
					// here makes file(1) report this Go source as a PDF,
					// which the binary-file commit hook then rejects.
					return nil, os.WriteFile(filepath.Join(args[i+1], "deck.pdf"), []byte("not read"), 0o600)
				}
			}
			return nil, errors.New("the fake reader was not told where to write")
		case "pdftotext":
			return []byte(words), nil
		}
		return nil, fmt.Errorf("unexpected command %q", name)
	}
	t.Cleanup(func() { pptxRunner = original })
}

// bboxXML builds pdftotext -bbox output from word boxes given as
// (xMin, yMin, xMax, yMax, text).
func bboxXML(words ...[5]string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><html><body><doc><page width="960.00" height="540.00">`)
	for _, w := range words {
		fmt.Fprintf(&b, `<word xMin="%s" yMin="%s" xMax="%s" yMax="%s">%s</word>`, w[0], w[1], w[2], w[3], w[4])
	}
	b.WriteString(`</page></doc></body></html>`)
	return b.String()
}

// writePPTX writes a one-slide package carrying a text box, which is all
// NativeSlides reads to decide a slide is editable.
func writePPTX(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "deck.pptx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create pptx: %v", err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("ppt/slides/slide1.xml")
	if err != nil {
		t.Fatalf("create slide part: %v", err)
	}
	if _, err := w.Write([]byte(`<p:sld><p:nvSpPr><p:cNvSpPr txBox="1"/></p:nvSpPr></p:sld>`)); err != nil {
		t.Fatalf("write slide part: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
	return path
}

func TestPPTXCheckRejectsBadFlags(t *testing.T) {
	if code, _, _ := exec("pptx-check", "-nonsense"); code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
}

func TestPPTXCheckRequiresThePPTXFlag(t *testing.T) {
	code, _, stderr := exec("pptx-check")
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "-pptx is required") {
		t.Errorf("stderr does not name the missing flag:\n%s", stderr)
	}
}

func TestPPTXCheckReportsAFileThatIsNotThere(t *testing.T) {
	code, _, stderr := exec("pptx-check", "-pptx", filepath.Join(t.TempDir(), "absent.pptx"))
	if code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr, "absent.pptx") {
		t.Errorf("stderr does not name the file:\n%s", stderr)
	}
}

func TestPPTXCheckIsDocumentedInUsage(t *testing.T) {
	// The subcommand was dispatchable for a release without appearing in
	// --help, because the switch and the usage text are maintained apart.
	_, stdout, _ := exec("--help")
	if !strings.Contains(stdout, "pptx-check") {
		t.Errorf("usage does not mention pptx-check:\n%s", stdout)
	}
}

func TestPPTXCheckOptionDefaultsComeFromThePackage(t *testing.T) {
	opts, err := parsePPTXCheckOptions([]string{"-pptx", "deck.pptx"}, os.Stderr)
	if err != nil {
		t.Fatalf("parsePPTXCheckOptions: %v", err)
	}
	for _, tc := range []struct {
		name      string
		got, want float64
	}{
		{"overlap", opts.overlap, pptxcheck.DefaultOverlapTolerancePt},
		{"min-type", opts.minType, pptxcheck.DefaultMinTypePt},
		{"edge", opts.edge, pptxcheck.DefaultEdgeTolerancePt},
		{"missing-word", opts.tolerance, pptxcheck.DefaultMissingWordTolerance},
	} {
		if tc.got != tc.want {
			t.Errorf("%s default = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}

func TestPPTXCheckPassesOnAFileWithNothingWrong(t *testing.T) {
	stubPPTXRunner(t, bboxXML(
		[5]string{"100", "100", "300", "120", "Comparativo"},
		[5]string{"100", "200", "300", "220", "entre"},
	))
	code, stdout, stderr := exec("pptx-check", "-pptx", writePPTX(t))
	if code != exitOK {
		t.Errorf("exit code = %d, want %d; stderr:\n%s", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, "ok") {
		t.Errorf("stdout does not report success:\n%s", stdout)
	}
}

func TestPPTXCheckFailsAndNamesTheRule(t *testing.T) {
	// Two lines in the same column, the upper one reaching well past the top
	// of the lower: the defect that prompted the whole measured check.
	stubPPTXRunner(t, bboxXML(
		[5]string{"100", "240", "500", "320", "Otimização"},
		[5]string{"100", "285", "400", "324", "Gestão"},
	))
	code, stdout, _ := exec("pptx-check", "-pptx", writePPTX(t))
	if code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	if !strings.Contains(stdout, pptxcheck.RuleOverlap) {
		t.Errorf("stdout does not name the rule that fired:\n%s", stdout)
	}
}

func TestPPTXCheckSaysWhichRulesCouldNotRun(t *testing.T) {
	// Without -deck-root there is nothing to compare the rendered words
	// against, so the strongest rule cannot run. Reporting plain "ok" there
	// is the failure the package exists to correct.
	stubPPTXRunner(t, bboxXML([5]string{"100", "100", "300", "120", "algo"}))
	code, stdout, _ := exec("pptx-check", "-pptx", writePPTX(t))
	if code != exitOK {
		t.Errorf("exit code = %d, want %d", code, exitOK)
	}
	for _, want := range []string{pptxcheck.RuleMissingText, "did not run"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout is missing %q:\n%s", want, stdout)
		}
	}
}

func TestPPTXCheckPropagatesADeckLoadFailure(t *testing.T) {
	// Resolved before the reader is launched, so a bad -deck-root fails fast
	// rather than after paying for a conversion.
	code, _, stderr := exec("pptx-check", "-pptx", writePPTX(t), "-deck-root", t.TempDir())
	if code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	if stderr == "" {
		t.Error("a deck that will not load must be reported")
	}
}

func TestPPTXCheckSurfacesAReaderFailure(t *testing.T) {
	original := pptxRunner
	pptxRunner = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("soffice exploded")
	}
	t.Cleanup(func() { pptxRunner = original })

	code, _, stderr := exec("pptx-check", "-pptx", writePPTX(t))
	if code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr, "soffice exploded") {
		t.Errorf("stderr does not surface the reader's failure:\n%s", stderr)
	}
}
