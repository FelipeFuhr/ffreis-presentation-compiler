package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/parity"
)

// writeParityDeck lays out a minimal deck directory (deck.yaml + one slide)
// and returns its root.
func writeParityDeck(t *testing.T, manifest, slide string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "deck.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write deck.yaml: %v", err)
	}
	dir := filepath.Join(root, "slides")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir slides: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "01.md"), []byte(slide), 0o600); err != nil {
		t.Fatalf("write slide: %v", err)
	}
	return root
}

// writeParityPNG writes a placeholder PNG for slide 01, matching the name
// internal/engine's PNG renderer uses. Content never matters: every test
// here stubs newParityRunner, so no real OCR ever reads the bytes.
func writeParityPNG(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "01.png"), []byte("<png bytes>"), 0o600); err != nil {
		t.Fatalf("write 01.png: %v", err)
	}
}

// stubParityLookPath makes parity.Available() deterministic, the same
// convention internal/parity's own tests use on the same package var.
func stubParityLookPath(t *testing.T, available bool) {
	t.Helper()
	original := parity.LookPath
	parity.LookPath = func(string) (string, error) {
		if available {
			return "/usr/bin/tesseract", nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() { parity.LookPath = original })
}

// stubParityRunner replaces newParityRunner so a test controls exactly what
// OCR "saw," without a real tesseract binary.
func stubParityRunner(t *testing.T, text string) {
	t.Helper()
	original := newParityRunner
	newParityRunner = func() parity.Runner {
		return func(context.Context, []byte, string) (string, error) { return text, nil }
	}
	t.Cleanup(func() { newParityRunner = original })
}

func TestParityRejectsBadFlags(t *testing.T) {
	if code, _, _ := exec("parity", "-nonsense"); code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
}

func TestParityRequiresDeckRoot(t *testing.T) {
	code, _, stderr := exec("parity", "-dir", t.TempDir())
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "-deck-root is required") {
		t.Errorf("stderr does not mention the missing flag:\n%s", stderr)
	}
}

func TestParityRequiresDir(t *testing.T) {
	code, _, stderr := exec("parity", "-deck-root", t.TempDir())
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "-dir is required") {
		t.Errorf("stderr does not mention the missing flag:\n%s", stderr)
	}
}

func TestParityOptionDefaults(t *testing.T) {
	opts, err := parseParityOptions([]string{"-deck-root", "d", "-dir", "x"}, os.Stderr)
	if err != nil {
		t.Fatalf("parseParityOptions: %v", err)
	}
	if opts.tolerance != parity.DefaultMissingWordTolerance {
		t.Errorf("tolerance = %v, want %v", opts.tolerance, parity.DefaultMissingWordTolerance)
	}
	if opts.lang != "" {
		t.Errorf("lang = %q, want empty so runParity resolves it from the deck", opts.lang)
	}
}

func TestParityIsReachableFromDispatch(t *testing.T) {
	stubParityLookPath(t, true)
	stubParityRunner(t, "")
	root := writeParityDeck(t, "title: T\n", "---\nlayout: cover\ntitle: X\n---\n")
	dir := t.TempDir()
	writeParityPNG(t, dir)

	code, _, _ := exec("parity", "-deck-root", root, "-dir", dir)
	if code == exitUsage {
		t.Error("parity is not wired into dispatch")
	}
}

func TestParityReportsWhenTheOCRToolIsMissing(t *testing.T) {
	stubParityLookPath(t, false)
	code, _, stderr := exec("parity", "-deck-root", t.TempDir(), "-dir", t.TempDir())
	if code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr, "tesseract") {
		t.Errorf("stderr does not name the missing tool:\n%s", stderr)
	}
}

func TestParityPropagatesADeckLoadFailure(t *testing.T) {
	stubParityLookPath(t, true)
	code, _, stderr := exec("parity", "-deck-root", filepath.Join(t.TempDir(), "absent"), "-dir", t.TempDir())
	if code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr, "parity:") {
		t.Errorf("stderr does not report the failure:\n%s", stderr)
	}
}

func TestParityPassesWhenOCRMatchesTheSource(t *testing.T) {
	stubParityLookPath(t, true)
	stubParityRunner(t, "Headline Words")
	root := writeParityDeck(t, "title: T\n", "---\nlayout: cover\ntitle: Headline Words\n---\n")
	dir := t.TempDir()
	writeParityPNG(t, dir)

	code, stdout, stderr := exec("parity", "-deck-root", root, "-dir", dir)
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, "slide 1: ok") {
		t.Errorf("stdout does not report the passing slide:\n%s", stdout)
	}
}

func TestParityFailsAndNamesTheMissingWords(t *testing.T) {
	stubParityLookPath(t, true)
	stubParityRunner(t, "") // OCR saw nothing at all
	root := writeParityDeck(t, "title: T\n",
		"---\nlayout: cover\ntitle: Dropped Headline Words\n---\n")
	dir := t.TempDir()
	writeParityPNG(t, dir)

	code, stdout, _ := exec("parity", "-deck-root", root, "-dir", dir, "-tolerance", "0")
	if code != exitError {
		t.Fatalf("exit code = %d, want %d\nstdout: %s", code, exitError, stdout)
	}
	for _, want := range []string{"FAIL", "dropped", "headline"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout %q does not contain %q", stdout, want)
		}
	}
}

func TestParityResolvesLangFromTheDeckWhenFlagIsOmitted(t *testing.T) {
	stubParityLookPath(t, true)
	var seenLang string
	original := newParityRunner
	newParityRunner = func() parity.Runner {
		return func(_ context.Context, _ []byte, lang string) (string, error) {
			seenLang = lang
			return "", nil
		}
	}
	t.Cleanup(func() { newParityRunner = original })

	root := writeParityDeck(t, "title: T\nlang: pt-BR\n", "---\nlayout: cover\ntitle: X\n---\n")
	dir := t.TempDir()
	writeParityPNG(t, dir)

	exec("parity", "-deck-root", root, "-dir", dir)
	if seenLang != "por" {
		t.Errorf("OCR lang = %q, want %q (derived from the deck's lang: pt-BR)", seenLang, "por")
	}
}

func TestParityLangFlagOverridesTheDeck(t *testing.T) {
	stubParityLookPath(t, true)
	var seenLang string
	original := newParityRunner
	newParityRunner = func() parity.Runner {
		return func(_ context.Context, _ []byte, lang string) (string, error) {
			seenLang = lang
			return "", nil
		}
	}
	t.Cleanup(func() { newParityRunner = original })

	root := writeParityDeck(t, "title: T\nlang: pt-BR\n", "---\nlayout: cover\ntitle: X\n---\n")
	dir := t.TempDir()
	writeParityPNG(t, dir)

	exec("parity", "-deck-root", root, "-dir", dir, "-lang", "spa")
	if seenLang != "spa" {
		t.Errorf("OCR lang = %q, want the explicit -lang override %q", seenLang, "spa")
	}
}
