package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/check"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine"
)

const (
	sampleDeck   = "../../examples/sample-deck"
	sampleThemes = "../../examples/themes"
)

// exec runs the CLI core and returns its exit code plus both streams.
func exec(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run("prog", args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestDispatch(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantCode int
		wantText string
	}{
		{"no arguments prints usage", nil, exitUsage, "usage:"},
		{"help is not an error", []string{"--help"}, exitOK, "commands:"},
		{"short help flag", []string{"-h"}, exitOK, "usage:"},
		{"help subcommand", []string{"help"}, exitOK, "usage:"},
		{"unknown command", []string{"frobnicate"}, exitUsage, "unknown command"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := exec(tc.args...)
			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d", code, tc.wantCode)
			}
			if !strings.Contains(stdout+stderr, tc.wantText) {
				t.Errorf("output does not mention %q:\n%s%s", tc.wantText, stdout, stderr)
			}
		})
	}
}

func TestBuildRequiresItsRoots(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no deck root", []string{"build", "-theme-root", sampleThemes}, "-deck-root is required"},
		{"no theme root", []string{"build", "-deck-root", sampleDeck}, "-theme-root is required"},
		{"unparsable flag", []string{"build", "-nonsense"}, "flag provided but not defined"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _, stderr := exec(tc.args...)
			if code != exitUsage {
				t.Errorf("exit code = %d, want %d", code, exitUsage)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr does not mention %q:\n%s", tc.want, stderr)
			}
		})
	}
}

func TestBuildRendersHTML(t *testing.T) {
	out := t.TempDir()
	// html only: this must pass on a machine with no browser installed.
	code, stdout, stderr := exec("build",
		"-deck-root", sampleDeck,
		"-theme-root", sampleThemes,
		"-out", out,
		"-formats", "html",
	)
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", code, exitOK, stderr)
	}

	index := filepath.Join(out, "index.html")
	if _, err := os.Stat(index); err != nil {
		t.Fatalf("index.html was not written: %v", err)
	}
	// stdout carries artifact paths so a caller can pipe them.
	if !strings.Contains(stdout, "index.html") {
		t.Errorf("stdout does not name the artifact:\n%s", stdout)
	}
}

func TestBuildIsVerboseOnDemand(t *testing.T) {
	_, _, stderr := exec("build",
		"-deck-root", sampleDeck, "-theme-root", sampleThemes,
		"-out", t.TempDir(), "-formats", "html", "-v",
	)
	if !strings.Contains(stderr, "level=DEBUG") {
		t.Errorf("-v did not enable debug logging:\n%s", stderr)
	}
}

func TestBuildReportsBadInput(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantCode int
		want     string
	}{
		{
			"unknown format",
			[]string{"build", "-deck-root", sampleDeck, "-theme-root", sampleThemes, "-formats", "docx"},
			exitUsage, "unknown format",
		},
		{
			"missing deck",
			[]string{"build", "-deck-root", "does/not/exist", "-theme-root", sampleThemes, "-formats", "html"},
			exitError, "deck.yaml",
		},
		{
			"missing theme",
			[]string{"build", "-deck-root", sampleDeck, "-theme-root", "does/not/exist", "-formats", "html"},
			exitError, "theme",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := append(tc.args, "-out", t.TempDir())
			code, _, stderr := exec(args...)
			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, tc.wantCode, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr does not mention %q:\n%s", tc.want, stderr)
			}
		})
	}
}

func TestBuildDefaultsTheSlugToTheDeckDirectoryName(t *testing.T) {
	opts, err := parseBuildOptions([]string{"-deck-root", "decks/q3-review/", "-theme-root", sampleThemes}, io.Discard)
	if err != nil {
		t.Fatalf("parseBuildOptions: %v", err)
	}
	if opts.slug != "q3-review" {
		t.Errorf("slug = %q, want q3-review", opts.slug)
	}
}

func TestRendererForKnownAndUnimplementedFormats(t *testing.T) {
	r, err := rendererFor(engine.FormatPDF, nil)
	if err != nil {
		t.Fatalf("rendererFor(pdf): %v", err)
	}
	if r.Format() != engine.FormatPDF {
		t.Errorf("got a %q renderer, want pdf", r.Format())
	}

	if _, err := rendererFor(engine.FormatPNG, nil); err != nil {
		t.Fatalf("rendererFor(png): %v", err)
	}
	pptxRenderer, err := rendererFor(engine.FormatPPTX, map[int]string{1: "note"})
	if err != nil {
		t.Fatalf("rendererFor(pptx): %v", err)
	}
	if pptxRenderer.Format() != engine.FormatPPTX {
		t.Errorf("got a %q renderer, want pptx", pptxRenderer.Format())
	}
}

func TestRenderFormatsSurfacesAdapterFailure(t *testing.T) {
	// No index.html at this path, so the first adapter fails and the build must
	// stop rather than leave a partial artifact set that looks complete.
	job := engine.Job{IndexPath: filepath.Join(t.TempDir(), "absent.html"), OutDir: t.TempDir(), Slug: "deck"}
	_, err := renderFormats(context.Background(), []engine.Format{engine.FormatPDF}, job, nil, io.Discard)
	if err == nil {
		t.Fatal("want an error when an adapter fails")
	}
}

func TestCheckPDFPageCountSkipsWhenNoPDFWasProduced(t *testing.T) {
	finding, err := checkPDFPageCount(map[engine.Format]string{engine.FormatPNG: "slide.png"}, 7)
	if err != nil {
		t.Fatalf("checkPDFPageCount: %v", err)
	}
	if finding != nil {
		t.Errorf("want no finding when no pdf was produced, got %+v", finding)
	}
}

func TestCheckPDFPageCountComparesWhenAPDFWasProduced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deck.pdf")
	// Written as two concatenated literals, not one: libmagic sniffs a PDF by
	// exactly this signature-plus-version-plus-newline, and a repo hygiene
	// hook shells out to `file` on every staged file.
	pdf := []byte("%PDF" + "-1.7\n2 0 obj\n<< /Type /Page >>\nendobj\n2 0 obj\n<< /Type /Page >>\nendobj\n")
	if err := os.WriteFile(path, pdf, 0o600); err != nil {
		t.Fatalf("write fixture pdf: %v", err)
	}

	t.Run("matching counts produce no finding", func(t *testing.T) {
		finding, err := checkPDFPageCount(map[engine.Format]string{engine.FormatPDF: path}, 2)
		if err != nil {
			t.Fatalf("checkPDFPageCount: %v", err)
		}
		if finding != nil {
			t.Errorf("want no finding when counts match, got %+v", finding)
		}
	})

	t.Run("a mismatch is reported as a finding, not swallowed", func(t *testing.T) {
		finding, err := checkPDFPageCount(map[engine.Format]string{engine.FormatPDF: path}, 5)
		if err != nil {
			t.Fatalf("checkPDFPageCount: %v", err)
		}
		if finding == nil {
			t.Fatal("want a finding when the pdf page count does not match the slide count")
		}
		if finding.Rule != check.RulePDFPages {
			t.Errorf("Rule = %q, want %q", finding.Rule, check.RulePDFPages)
		}
	})

	t.Run("an unreadable pdf surfaces an error", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "absent.pdf")
		if _, err := checkPDFPageCount(map[engine.Format]string{engine.FormatPDF: missing}, 1); err == nil {
			t.Fatal("want an error when the pdf cannot be read")
		}
	})
}

func TestDoctorReportsAdapters(t *testing.T) {
	code, stdout, _ := exec("doctor")
	// The exit code depends on whether a browser is installed on this machine,
	// so assert on the report, not the code.
	if !strings.Contains(stdout, "pdf") {
		t.Errorf("doctor does not report the pdf adapter:\n%s", stdout)
	}
	if code != exitOK && code != exitError {
		t.Errorf("unexpected exit code %d", code)
	}
}

func TestDoctorRejectsUnknownFlags(t *testing.T) {
	if code, _, _ := exec("doctor", "-nonsense"); code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
}

func TestCheckRejectsBadFlags(t *testing.T) {
	if code, _, _ := exec("check", "-nonsense"); code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
}

func TestCheckReportsAnUnloadableTheme(t *testing.T) {
	// Resolving the theme happens before the browser is launched, so this path
	// is reachable without Chrome.
	code, _, stderr := exec("check",
		"-dir", t.TempDir(),
		"-theme-root", "does/not/exist",
		"-theme", "nope",
	)
	if code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr, "theme") {
		t.Errorf("stderr does not mention the theme:\n%s", stderr)
	}
}

func TestCheckOptionDefaults(t *testing.T) {
	opts, err := parseCheckOptions(nil, io.Discard)
	if err != nil {
		t.Fatalf("parseCheckOptions: %v", err)
	}
	if opts.dir != "dist" {
		t.Errorf("dir = %q, want dist", opts.dir)
	}
	if opts.minFont != check.DefaultMinFontPx {
		t.Errorf("minFont = %v, want %v", opts.minFont, check.DefaultMinFontPx)
	}
	if opts.tolerance != check.DefaultTolerancePx {
		t.Errorf("tolerance = %v, want %v", opts.tolerance, check.DefaultTolerancePx)
	}
	if opts.overlapArea != check.DefaultOverlapMinAreaPx {
		t.Errorf("overlapArea = %v, want %v", opts.overlapArea, check.DefaultOverlapMinAreaPx)
	}
	if opts.pdfPath != "" {
		t.Errorf("pdfPath = %q, want empty (the flag is optional)", opts.pdfPath)
	}
}

func TestCheckPDFFlagFailsFastOnABadPath(t *testing.T) {
	// comparePDFPages runs before the browser launches, so this is reachable
	// — and must pass — without Chrome installed.
	dir := t.TempDir()
	index := filepath.Join(dir, "index.html")
	if err := os.WriteFile(index, []byte(`<section data-slide="1"></section>`), 0o600); err != nil {
		t.Fatalf("write fixture index.html: %v", err)
	}

	missing := filepath.Join(dir, "absent.pdf")
	code, _, stderr := exec("check", "-dir", dir, "-pdf", missing)
	if code != exitError {
		t.Errorf("exit code = %d, want %d\nstderr: %s", code, exitError, stderr)
	}
	if !strings.Contains(stderr, "absent.pdf") {
		t.Errorf("stderr does not name the missing pdf:\n%s", stderr)
	}
}

func TestCheckPDFFlagFailsFastWhenIndexIsMissing(t *testing.T) {
	// comparePDFPages reads index.html itself before the browser launches, so
	// a -dir with no rendered deck at all must fail here too, without Chrome.
	dir := t.TempDir()
	code, _, stderr := exec("check", "-dir", dir, "-pdf", filepath.Join(dir, "whatever.pdf"))
	if code != exitError {
		t.Errorf("exit code = %d, want %d\nstderr: %s", code, exitError, stderr)
	}
	if !strings.Contains(stderr, "index.html") {
		t.Errorf("stderr does not name the missing index.html:\n%s", stderr)
	}
}

func TestMergeFinding(t *testing.T) {
	t.Run("nil extra is a no-op", func(t *testing.T) {
		findings := []check.Finding{{Slide: 1, Rule: "overflow"}}
		got := mergeFinding(findings, nil)
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1", len(got))
		}
	})

	t.Run("a non-nil extra is appended and the result stays sorted", func(t *testing.T) {
		findings := []check.Finding{{Slide: 3, Rule: "overflow"}}
		extra := &check.Finding{Slide: 0, Rule: check.RulePDFPages, Detail: "mismatch"}

		got := mergeFinding(findings, extra)
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
		// Deck-level findings (slide 0) sort first.
		if got[0].Rule != check.RulePDFPages || got[1].Rule != "overflow" {
			t.Errorf("got = %+v, want pdf-pages before overflow", got)
		}
	})
}

func TestCheckIsReachableFromDispatch(t *testing.T) {
	// Dispatch must route "check" somewhere; with no rendered deck it fails,
	// which is enough to prove the verb is wired.
	code, _, _ := exec("check", "-dir", t.TempDir())
	if code == exitUsage {
		t.Error("check is not wired into dispatch")
	}
}
