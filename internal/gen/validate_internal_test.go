package gen

import (
	"errors"
	"strings"
	"testing"
)

// In-package because validateArtifact is unexported and sniffing a format is
// an implementation detail of this package's own silent-failure guard, not
// something it should promise anyone — same reason internal/golden keeps
// slidelabel_internal_test.go.

func TestValidateArtifactAcceptsRealisticSVG(t *testing.T) {
	svg := "<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 100 100\">" +
		strings.Repeat("<circle cx=\"5\" cy=\"5\" r=\"2\"/>", 20) +
		"</svg>"
	if err := validateArtifact("svg", []byte(svg)); err != nil {
		t.Errorf("validateArtifact rejected a well-formed, large-enough SVG: %v", err)
	}
}

func TestValidateArtifactRejectsASelfClosingEmptySVGRoot(t *testing.T) {
	// <svg/> with no children is exactly what a generator that crashed before
	// drawing anything, but still exited 0, would leave behind.
	padded := "<svg xmlns=\"http://www.w3.org/2000/svg\"/>" + strings.Repeat(" ", 300)
	if err := validateArtifact("svg", []byte(padded)); err == nil {
		t.Error("want an error for a self-closing <svg/> root with no </svg> close tag")
	}
}

func TestValidateArtifactAcceptsARealPNGSignature(t *testing.T) {
	data := append([]byte{}, pngSignature...)
	data = append(data, []byte(strings.Repeat("x", 300))...) // past MinArtifactBytes
	if err := validateArtifact("png", data); err != nil {
		t.Errorf("validateArtifact rejected data starting with a real PNG signature: %v", err)
	}
}

func TestValidateArtifactRejectsDataWithoutAPNGSignature(t *testing.T) {
	data := []byte(strings.Repeat("not a png at all, just padding", 20))
	if err := validateArtifact("png", data); err == nil {
		t.Error("want an error for data with no PNG signature")
	}
}

func TestValidateArtifactSkipsFormatSniffingForAnUnknownExtension(t *testing.T) {
	// The format-sniffing switch only knows svg and png; anything else falls
	// through with only the size floor applied, rather than rejecting a
	// format this package was never told how to recognise.
	data := []byte(strings.Repeat("whatever this format's bytes look like, ", 10))
	if err := validateArtifact("txt", data); err != nil {
		t.Errorf("validateArtifact(%q) rejected an unrecognised-but-large-enough format: %v", "txt", err)
	}
}

func TestValidateArtifactRejectsAnythingUnderTheSizeFloorRegardlessOfFormat(t *testing.T) {
	if err := validateArtifact("txt", []byte("tiny")); err == nil {
		t.Error("want the size floor to apply even to a format this package does not sniff")
	}
}

// fakeTokenInjector lets a test exercise prepareSpec's error-handling branch
// for InjectTokens failing — d2Generator's own implementation never does, so
// this is the only way to prove the branch behaves rather than merely exists.
type fakeTokenInjector struct {
	Generator
	err error
}

func (f fakeTokenInjector) InjectTokens([]byte, map[string]string) ([]byte, error) {
	return nil, f.err
}

func TestPrepareSpecPropagatesATokenInjectorFailure(t *testing.T) {
	boom := errors.New("boom")
	_, cleanup, err := prepareSpec(fakeTokenInjector{Generator: python, err: boom}, "spec.d2", []byte("x"), nil)
	defer cleanup()
	if !errors.Is(err, boom) {
		t.Errorf("prepareSpec error = %v, want it to wrap %v", err, boom)
	}
}
