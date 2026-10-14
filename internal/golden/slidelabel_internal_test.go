package golden

import "testing"

// TestSlideLabelFallsBackToTheFilename covers the branch that matters when a
// baseline is named anything other than NN.png: guessing a slide number from
// an unparseable name would put the wrong slide in a failure report, which is
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
