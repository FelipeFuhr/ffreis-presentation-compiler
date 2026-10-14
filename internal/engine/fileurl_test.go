package engine_test

import (
	"net/url"
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
