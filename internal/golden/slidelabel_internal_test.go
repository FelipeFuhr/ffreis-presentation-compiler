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
