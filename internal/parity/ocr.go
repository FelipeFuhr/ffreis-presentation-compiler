// Package parity OCRs a deck's own rendered slide PNG files and compares the
// recognised text against the deck's authored source — kicker, title, body
// prose, and every string in a layout's own fields — so a section or a short
// annotation dropped during authoring or a port shows up as a build-time
// finding instead of something only a line-by-line read catches.
//
// This measures something internal/check and internal/golden cannot: check
// measures geometry (does the text fit), golden measures pixels (did the
// image change) — neither has an opinion on whether the rendered words are
// the ones the deck actually declares. A table that fits the slide and
// passes every contrast and overflow rule can still be a table of the wrong
// numbers; see internal/contract's row-arity rule for one mechanical cause
// of exactly that, and this package for the general case no mechanical rule
// can name in advance.
package parity

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Tool is the external OCR engine this package shells out to.
const Tool = "tesseract"

// Runner invokes an OCR engine over png and returns its recognised plain
// text. Injected so tests need no toolchain installed, the same trick as
// gen.Runner and the chrome client's launch field.
type Runner func(ctx context.Context, png []byte, lang string) (string, error)

// LookPath is indirected so tests can simulate a missing toolchain, the same
// convention internal/gen's LookPath and the chrome client's launch field use.
var LookPath = func(name string) (string, error) { return exec.LookPath(name) }

// Available reports whether the OCR tool is on PATH.
func Available() bool {
	_, err := LookPath(Tool)
	return err == nil
}

// ExecRunner is the real Runner: tesseract's own stdin/stdout contract
// (`tesseract stdin stdout -l <lang>`) needs no temp files — it reads the
// image from stdin and writes recognised plain text to stdout, keeping its
// own diagnostic logging ("Estimating resolution as …") on stderr, which is
// why that stream is captured separately rather than merged in.
func ExecRunner(ctx context.Context, png []byte, lang string) (string, error) {
	cmd := exec.CommandContext(ctx, Tool, "stdin", "stdout", "-l", lang) //nolint:gosec // invoking the configured OCR binary is the purpose of this adapter
	cmd.Stdin = bytes.NewReader(png)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w: %s", Tool, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// DefaultOCRLang is used when a deck's language tag maps to no known
// tesseract model.
const DefaultOCRLang = "eng"

// bcp47ToTesseract maps a BCP 47 primary language subtag to the ISO 639-2/T
// code tesseract's -l flag expects. Small and deliberately incomplete: it
// covers languages a presentation deck is plausibly authored in, not every
// model tesseract ships. An unmapped tag falls back to DefaultOCRLang rather
// than failing the build: recognition quality on the wrong language model
// degrades (accents get mangled — a 'ç' misread as a stray mark — rather
// than the run erroring out), which is the right failure mode for a report
// that is already fuzzy by nature.
var bcp47ToTesseract = map[string]string{
	"en": "eng", "pt": "por", "es": "spa", "fr": "fra", "de": "deu",
	"it": "ita", "nl": "nld", "sv": "swe", "da": "dan", "no": "nor",
	"pl": "pol", "ro": "ron", "ca": "cat",
}

// OCRLang resolves a deck's BCP 47 language tag (as deck.Deck.Language
// already resolves it, with override/default applied) to a tesseract
// language code, taking only the primary subtag ("pt" from "pt-BR") since
// tesseract's trained data is not region-specific.
func OCRLang(bcp47 string) string {
	primary, _, _ := strings.Cut(bcp47, "-")
	if code, ok := bcp47ToTesseract[strings.ToLower(primary)]; ok {
		return code
	}
	return DefaultOCRLang
}
