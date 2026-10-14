package engine_test

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine"
)

// TestFileURLRoundTrips locks in the fix for "file://" + path string
// concatenation: every one of these paths broke the naive concatenation in a
// different way ('#' truncates as a fragment, '?' starts a query, '%' begins
// a percent-escape, a space is unreliable), so each must survive a round trip
// through url.Parse back to the exact original filesystem path.
func TestFileURLRoundTrips(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		file string
	}{
		{"plain path", "index.html"},
		{"space", "my deck/index.html"},
		{"hash", "deck#1/index.html"},
		{"percent", "100% ready/index.html"},
		{"non-ASCII", "Grêmio/index.html"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			abs := filepath.Join(dir, tc.file)

			got, err := engine.FileURL(abs)
			if err != nil {
				t.Fatalf("FileURL(%q): %v", abs, err)
			}

			parsed, err := url.Parse(got)
			if err != nil {
				t.Fatalf("url.Parse(%q): %v", got, err)
			}
			if parsed.Scheme != "file" {
				t.Errorf("scheme = %q, want %q", parsed.Scheme, "file")
			}
			if parsed.Path != abs {
				t.Errorf("FileURL(%q) = %q, parsed back to path %q", abs, got, parsed.Path)
			}
		})
	}
}

// TestFileURLWrapsAnAbsFailure forces filepath.Abs's only failure mode —
// os.Getwd() erroring — by chdir'ing into an empty directory and removing it
// out from under the process, then calling FileURL with a relative path.
//
// This is exercised directly against FileURL rather than through any of its
// callers (the PDF/PNG/PPTX adapters, the check subcommand) because every one
// of them calls os.Stat or filepath.Abs on the same path *before* reaching
// FileURL; with a dead working directory that earlier call fails for the
// identical reason first, so the caller's own FileURL-error branch can never
// be reached from outside the package. Verified empirically, not assumed:
// with cwd removed, `os.Stat("x")` itself already returns
// `stat x: no such file or directory` before FileURL's internal
// `filepath.Abs` is ever reached.
func TestFileURLWrapsAnAbsFailure(t *testing.T) {
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	dead := t.TempDir()
	if err := os.Chdir(dead); err != nil {
		t.Fatalf("chdir into %s: %v", dead, err)
	}
	if err := os.Remove(dead); err != nil {
		t.Fatalf("remove the working directory out from under the process: %v", err)
	}

	if _, err := engine.FileURL("relative/index.html"); err == nil {
		t.Fatal("want an error when the working directory no longer exists")
	}
}

// TestFileURLAbsolutizesARelativePath asserts a relative input does not
// produce a file:// URL that silently resolves against nothing; FileURL
// absolutizes it against the working directory instead.
func TestFileURLAbsolutizesARelativePath(t *testing.T) {
	rel := "relative/index.html"
	want, err := filepath.Abs(rel)
	if err != nil {
		t.Fatalf("filepath.Abs(%q): %v", rel, err)
	}

	got, err := engine.FileURL(rel)
	if err != nil {
		t.Fatalf("FileURL(%q): %v", rel, err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", got, err)
	}
	if parsed.Path != want {
		t.Errorf("FileURL(%q) resolved to %q, want the absolutized %q", rel, parsed.Path, want)
	}
	if !filepath.IsAbs(parsed.Path) {
		t.Errorf("FileURL(%q) produced a non-absolute path %q", rel, parsed.Path)
	}
}
