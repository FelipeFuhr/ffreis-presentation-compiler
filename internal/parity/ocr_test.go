package parity_test

import (
	"errors"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/parity"
)

func TestAvailableReflectsLookPath(t *testing.T) {
	original := parity.LookPath
	t.Cleanup(func() { parity.LookPath = original })

	parity.LookPath = func(string) (string, error) { return "/usr/bin/tesseract", nil }
	if !parity.Available() {
		t.Error("Available() = false with a stubbed-present tool")
	}
	parity.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	if parity.Available() {
		t.Error("Available() = true with a stubbed-missing tool")
	}
}

func TestOCRLangMapsThePrimarySubtag(t *testing.T) {
	cases := map[string]string{
		"pt-BR": "por",
		"pt":    "por",
		"en":    "eng",
		"en-US": "eng",
		"es-AR": "spa",
		"":      parity.DefaultOCRLang,
		"xx-YY": parity.DefaultOCRLang, // unmapped: falls back, does not error
	}
	for in, want := range cases {
		if got := parity.OCRLang(in); got != want {
			t.Errorf("OCRLang(%q) = %q, want %q", in, got, want)
		}
	}
}
