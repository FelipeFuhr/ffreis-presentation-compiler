package fontsource_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/fontsource"
)

// writeFontDir creates a directory holding the named (empty) font files.
//
// Contents are irrelevant: this package classifies by filename and extension
// and never opens a font. Parsing one is engine/pptx's job, and keeping the
// two apart is why a wrong-weight filename is caught here and a malformed
// font is caught there.
func writeFontDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("font"), 0o600); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}
	return dir
}

// fakeFc answers fc-match from a table keyed by the pattern it is asked.
func fakeFc(answers map[string]string) fontsource.Runner {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != fontsource.Tool {
			return nil, fmt.Errorf("unexpected command %q", name)
		}
		pattern := args[len(args)-1]
		if a, ok := answers[pattern]; ok {
			return []byte(a), nil
		}
		// What fc-match really does for a face it does not have: answer with
		// the system's best substitute rather than nothing.
		return []byte("/usr/share/fonts/DejaVuSans.ttf\tDejaVu Sans\tBook"), nil
	}
}

func variants(faces []fontsource.Face) string {
	out := make([]string, 0, len(faces))
	for _, f := range faces {
		out = append(out, string(f.Variant))
	}
	return strings.Join(out, ",")
}

func resolve(t *testing.T, typeface string, opt fontsource.Options) ([]fontsource.Face, []string) {
	t.Helper()
	faces, notes, err := fontsource.Resolve(context.Background(), typeface, opt)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return faces, notes
}

// ── directories ─────────────────────────────────────────────────────────────

func TestAVendoredDirectorySuppliesEveryVariant(t *testing.T) {
	dir := writeFontDir(t, "Roboto-Regular.ttf", "Roboto-Bold.ttf", "Roboto-Italic.otf", "Roboto-BoldItalic.ttf")
	faces, _ := resolve(t, "Roboto", fontsource.Options{Dirs: []string{dir}})
	if got := variants(faces); got != "regular,bold,italic,boldItalic" {
		t.Errorf("variants = %q, want all four in declaration order", got)
	}
	for _, f := range faces {
		if f.Source != dir {
			t.Errorf("%s: Source = %q, want the directory it came from", f.Variant, f.Source)
		}
	}
}

func TestAnEarlierDirectoryWins(t *testing.T) {
	// A theme that vendors a file meant that file, whatever else is around.
	first := writeFontDir(t, "Roboto-Regular.ttf")
	second := writeFontDir(t, "Roboto-Regular.ttf", "Roboto-Bold.ttf")
	faces, _ := resolve(t, "Roboto", fontsource.Options{Dirs: []string{first, second}})
	for _, f := range faces {
		if f.Variant == fontsource.Regular && f.Source != first {
			t.Errorf("regular came from %q, want the first directory", f.Source)
		}
	}
}

func TestAnUnknownWeightSuffixIsSkipped(t *testing.T) {
	// Embedding a Medium or a Light as the regular weight would put a
	// typeface in the deck that the author did not choose.
	dir := writeFontDir(t, "Roboto-Medium.ttf", "Roboto-Light.ttf", "Roboto-300.ttf",
		"Roboto.ttf", "Roboto-Bold.ttf")
	faces, _ := resolve(t, "Roboto", fontsource.Options{Dirs: []string{dir}})
	if got := variants(faces); got != "bold" {
		t.Errorf("variants = %q, want only bold", got)
	}
}

func TestAMissingDirectoryIsNotAnError(t *testing.T) {
	// The vendored path is optional by design; most themes will not have one.
	faces, _ := resolve(t, "Roboto", fontsource.Options{
		Dirs: []string{filepath.Join(t.TempDir(), "absent")},
	})
	if len(faces) != 0 {
		t.Errorf("got %d faces from a directory that does not exist", len(faces))
	}
}

func TestWebFontsAreNamedAsUnusable(t *testing.T) {
	// A theme that self-hosts Roboto has .woff2 and nothing else, which looks
	// like it ought to work. Saying so is the difference between a build the
	// author can fix and a deck that silently substitutes.
	dir := writeFontDir(t, "Roboto-Regular.woff2", "Roboto-Bold.woff2")
	faces, notes := resolve(t, "Roboto", fontsource.Options{Dirs: []string{dir}})
	if len(faces) != 0 {
		t.Errorf("a .woff2 was offered for embedding: %v", faces)
	}
	joined := strings.Join(notes, "\n")
	if !strings.Contains(joined, "cannot carry") {
		t.Errorf("notes do not explain why the web fonts were skipped: %v", notes)
	}
	// And they name WHICH variants, since that is what the reader has to act on.
	for _, want := range []string{"regular", "bold"} {
		if !strings.Contains(joined, want) {
			t.Errorf("notes do not name the %s variant: %v", want, notes)
		}
	}
}

func TestAWebFontIsNotMentionedWhenSomethingElseSuppliesThatVariant(t *testing.T) {
	// A .woff2 sitting beside the .ttf that was actually used is not worth a
	// word. The note is advice, and there is nothing to act on.
	dir := writeFontDir(t, "Roboto-Regular.ttf", "Roboto-Regular.woff2",
		"Roboto-Bold.ttf", "Roboto-Italic.ttf", "Roboto-BoldItalic.ttf")
	faces, notes := resolve(t, "Roboto", fontsource.Options{Dirs: []string{dir}})
	if len(faces) != 4 {
		t.Fatalf("got %d faces, want all four", len(faces))
	}
	if len(notes) != 0 {
		t.Errorf("a fully-resolved typeface produced notes: %v", notes)
	}
}

func TestAWebFontIsNotBlamedForAVariantItDoesNotHave(t *testing.T) {
	// The real case this narrowing fixes. A theme self-hosts regular, bold
	// and italic as .woff2 and vendors the same three as .ttf; the variant
	// still missing is boldItalic, which the web fonts do not carry either.
	//
	// Fired on any unfilled variant, the note told a theme that had already
	// shipped the right .ttf files to ship them again.
	dir := writeFontDir(t,
		"Roboto-Regular.ttf", "Roboto-Bold.ttf", "Roboto-Italic.ttf",
		"Roboto-Regular.woff2", "Roboto-Bold.woff2", "Roboto-Italic.woff2")
	_, notes := resolve(t, "Roboto", fontsource.Options{Dirs: []string{dir}})

	joined := strings.Join(notes, "\n")
	if strings.Contains(joined, "web font") {
		t.Errorf("blamed the web fonts for a variant they do not have: %v", notes)
	}
	// The honest note remains: one variant has no file anywhere.
	if !strings.Contains(joined, "no file found for boldItalic") {
		t.Errorf("notes do not report the genuinely missing variant: %v", notes)
	}
}

// ── the system ──────────────────────────────────────────────────────────────

func TestTheSystemSuppliesWhatNoDirectoryHas(t *testing.T) {
	// The case this package was written for: a face the author has installed
	// but has not vendored.
	faces, _ := resolve(t, "Roboto", fontsource.Options{Runner: fakeFc(map[string]string{
		"Roboto:style=Regular": "/fonts/Roboto-Regular.ttf\tRoboto\tRegular",
		"Roboto:style=Bold":    "/fonts/Roboto-Bold.ttf\tRoboto,Roboto Bold\tBold",
	})})
	if got := variants(faces); got != "regular,bold" {
		t.Errorf("variants = %q, want regular,bold from the system", got)
	}
	for _, f := range faces {
		if !strings.Contains(f.Source, "installed") {
			t.Errorf("%s: Source = %q, want it to say the machine supplied it", f.Variant, f.Source)
		}
	}
}

func TestFontconfigsSubstituteIsRejected(t *testing.T) {
	// fc-match always answers. Asked for a face that is not installed it
	// returns the system's best substitute, so trusting it would embed
	// DejaVu Sans under the name Roboto and the deck would be wrong in a way
	// no later check could explain.
	faces, _ := resolve(t, "Roboto", fontsource.Options{Runner: fakeFc(nil)})
	if len(faces) != 0 {
		t.Errorf("fontconfig's substitute was accepted as Roboto: %v", faces)
	}
}

func TestAWrongStyleFromFontconfigIsRejected(t *testing.T) {
	// A family can be installed without the weight asked for, and fc-match
	// answers with the regular face. Embedding that as the bold would make
	// every bold run in the deck render light.
	faces, _ := resolve(t, "Roboto", fontsource.Options{Runner: fakeFc(map[string]string{
		"Roboto:style=Regular": "/fonts/Roboto-Regular.ttf\tRoboto\tRegular",
		"Roboto:style=Bold":    "/fonts/Roboto-Regular.ttf\tRoboto\tRegular",
	})})
	if got := variants(faces); got != "regular" {
		t.Errorf("variants = %q, want only regular; the bold slot was filled with a regular face", got)
	}
}

func TestAnInstalledWebFontCannotBeEmbedded(t *testing.T) {
	faces, notes := resolve(t, "Roboto", fontsource.Options{Runner: fakeFc(map[string]string{
		"Roboto:style=Regular": "/fonts/Roboto-Regular.woff2\tRoboto\tRegular",
	})})
	if len(faces) != 0 {
		t.Errorf("an installed .woff2 was offered for embedding: %v", faces)
	}
	if !strings.Contains(strings.Join(notes, "\n"), "cannot carry") {
		t.Errorf("notes do not say why: %v", notes)
	}
}

func TestTheSystemIsNotConsultedWithoutARunner(t *testing.T) {
	// A build wanting only vendored fonts asks for exactly this, and it is
	// also what keeps these tests independent of the machine running them.
	faces, _ := resolve(t, "Roboto", fontsource.Options{})
	if len(faces) != 0 {
		t.Errorf("got %d faces with no Runner", len(faces))
	}
}

func TestAFailingFontconfigIsReportedNotSwallowed(t *testing.T) {
	run := func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("fc-match exploded")
	}
	_, notes := resolve(t, "Roboto", fontsource.Options{Runner: run})
	if !strings.Contains(strings.Join(notes, "\n"), "fc-match exploded") {
		t.Errorf("a failing font query was swallowed: %v", notes)
	}
}

func TestADirectoryIsPreferredOverTheSystem(t *testing.T) {
	dir := writeFontDir(t, "Roboto-Regular.ttf")
	faces, _ := resolve(t, "Roboto", fontsource.Options{
		Dirs:   []string{dir},
		Runner: fakeFc(map[string]string{"Roboto:style=Regular": "/elsewhere/Roboto-Regular.ttf\tRoboto\tRegular"}),
	})
	for _, f := range faces {
		if f.Variant == fontsource.Regular && f.Source != dir {
			t.Errorf("regular came from %q, want the vendored directory", f.Source)
		}
	}
}

// ── the contract ────────────────────────────────────────────────────────────

func TestAnEmptyTypefaceIsAnError(t *testing.T) {
	if _, _, err := fontsource.Resolve(context.Background(), "   ", fontsource.Options{}); err == nil {
		t.Error("want an error for a typeface with no name")
	}
}

func TestMissingVariantsAreNamed(t *testing.T) {
	// Three of four is a better outcome than refusing the lot, since
	// PowerPoint can synthesise a slope, but the build has to say which.
	dir := writeFontDir(t, "Roboto-Regular.ttf")
	_, notes := resolve(t, "Roboto", fontsource.Options{Dirs: []string{dir}})
	joined := strings.Join(notes, "\n")
	for _, want := range []string{"bold", "italic", "boldItalic"} {
		if !strings.Contains(joined, want) {
			t.Errorf("notes do not name the missing %s variant: %v", want, notes)
		}
	}
}
