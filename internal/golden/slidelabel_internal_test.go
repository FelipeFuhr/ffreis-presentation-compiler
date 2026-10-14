package golden

import (
	"image"
	"os"
	"path/filepath"
	"testing"
)

// TestSlideLabelFallsBackToTheFilename covers the branch that matters when a
// baseline is named anything other than NN.png: guessing a slide number from
// an unparsable name would put the wrong slide in a failure report, which is
// worse than printing the filename as-is.
//
// In-package because slideLabel is unexported and formatting a label is not
// something this package should promise anyone — same reason
// internal/render keeps copydir_internal_test.go.
func TestSlideLabelFallsBackToTheFilename(t *testing.T) {
	cases := map[string]string{
		"04.png":       "slide 4",
		"1.png":        "slide 1",
		"cover.png":    "cover.png",
		"04-cover.png": "04-cover.png",
		"":             "",
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			if got := slideLabel(name); got != want {
				t.Errorf("slideLabel(%q) = %q, want %q", name, got, want)
			}
		})
	}
}

func TestWriteDiffImageReportsAnUnwritableDirectory(t *testing.T) {
	// The diff artifact is the only thing a human can look at after a golden
	// failure, so failing to write it must surface, not be swallowed.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed a regular file where a directory is wanted: %v", err)
	}

	_, err := writeDiffImage(filepath.Join(blocker, "sub"), "04.png",
		image.NewNRGBA(image.Rect(0, 0, 2, 2)))

	if err == nil {
		t.Fatal("want an error writing a diff under a path that is a regular file")
	}
}

// TestBaselineNamesIgnoresNonPNGEntries covers the skip branch: a stray
// README or a nested directory in the baseline folder must not be mistaken
// for a slide, which would report a phantom failure on every run.
func TestBaselineNamesIgnoresNonPNGEntries(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"02.png", "01.png", "README.md", ".gitkeep"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o750); err != nil {
		t.Fatalf("seed a nested dir: %v", err)
	}

	names, err := baselineNames(dir)
	if err != nil {
		t.Fatalf("baselineNames: %v", err)
	}

	want := []string{"01.png", "02.png"}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("names[%d] = %q, want %q (sorted, so a report reads in slide order)", i, names[i], want[i])
		}
	}
}

// TestDecodePNGRejectsAFileThatIsNotAPNG keeps a corrupt or truncated
// baseline from surfacing as a confusing zero-pixel diff.
func TestDecodePNGRejectsAFileThatIsNotAPNG(t *testing.T) {
	path := filepath.Join(t.TempDir(), "04.png")
	if err := os.WriteFile(path, []byte("definitely not a png"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := decodePNG(path); err == nil {
		t.Fatal("want an error decoding a file that is not a PNG")
	}
}
