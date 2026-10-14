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
		{"non-ASCII", "Diseño Único/index.html"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			abs := filepath.Join(dir, tc.file)

			got := engine.FileURL(abs)

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

// TestFileURLFallsBackWhenTheWorkingDirectoryIsGone forces filepath.Abs's
// only failure mode — os.Getwd() erroring — by chdir'ing into an empty
// directory and removing it out from under the process, then calling FileURL
// with a relative path.
//
// FileURL deliberately has no error return: every caller stats the same path
// first, so a dead working directory surfaces there with a better message,
// and an error here only bought four callers an unreachable branch. This
// locks in the fallback instead — the path is used as given rather than the
// function panicking or emitting a "file://" URL with an empty path.
func TestFileURLFallsBackWhenTheWorkingDirectoryIsGone(t *testing.T) {
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

	got := engine.FileURL("relative/index.html")
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", got, err)
	}
	// Root-relative, not bare: url.URL renders a Path with no leading
	// separator as "file://relative/index.html", which parses back with
	// Host="relative" and Path="/index.html" — silently wrong. Guard that.
	if parsed.Host != "" {
		t.Errorf("FileURL(%q) produced a URL with Host=%q; the path was misparsed as a host", got, parsed.Host)
	}
	if parsed.Path != "/relative/index.html" {
		t.Errorf("FileURL fell back to %q, want the path made root-relative", parsed.Path)
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

	got := engine.FileURL(rel)
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
