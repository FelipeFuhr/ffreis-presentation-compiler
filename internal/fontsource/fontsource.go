// Package fontsource finds the font files that back a typeface.
//
// It exists because a theme names a typeface and a .pptx has to carry the
// bytes. Those bytes can come from three places, and which one answered
// matters enough to report: a directory the theme vendors, the theme's own
// web-font directory, or the fonts installed on the machine running the
// build.
//
// The last of those is the point. A deck set in a face the author has
// installed but has not vendored is the normal case, not an edge case, and
// refusing to look there means the only typefaces that can be embedded are
// the ones somebody remembered to copy into the repo. It is also the least
// reproducible source, which is why Resolve says where each face came from
// rather than just handing back bytes.
package fontsource

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Variant is a weight and slope a PPTX can embed. These four are the whole
// set OOXML models: a face with more weights has to pick which fill them.
type Variant string

const (
	Regular    Variant = "regular"
	Bold       Variant = "bold"
	Italic     Variant = "italic"
	BoldItalic Variant = "boldItalic"
)

// Variants in declaration order, so output is stable.
func Variants() []Variant { return []Variant{Regular, Bold, Italic, BoldItalic} }

// fontconfigStyle is how each variant is spelled in a fontconfig pattern.
var fontconfigStyle = map[Variant]string{
	Regular: "Regular", Bold: "Bold", Italic: "Italic", BoldItalic: "Bold Italic",
}

// filenameSuffix is how each variant is spelled at the end of a filename,
// lowercased. `Roboto-BoldItalic.ttf` and `Roboto-bolditalic.otf` both match.
var filenameSuffix = map[string]Variant{
	"regular": Regular, "bold": Bold, "italic": Italic, "bolditalic": BoldItalic,
}

// Face is one resolved variant and where it was found.
type Face struct {
	Variant Variant
	Path    string
	// Source is provenance for a human: which directory, or the system.
	Source string
}

// Runner shells out and returns standard output.
//
// Injected for the reason engine.Runner is: every test here runs against
// canned output, so none of them needs fontconfig installed to decide
// whether it passes -- and a machine whose font set differs cannot change
// the result.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Options says where to look.
type Options struct {
	// Dirs are searched in order, before the system is consulted. A face
	// found in an earlier directory wins: a theme that vendors a file meant
	// that file, whatever happens to be installed.
	Dirs []string

	// Runner reaches fontconfig. Nil disables the system search, which is
	// what a build wanting only vendored fonts asks for.
	Runner Runner
}

// sfntExts are the font containers a .pptx can carry.
//
// .woff and .woff2 are deliberately absent. They are compressed wrappers
// around the same tables, so they look like they ought to work, and a theme
// that self-hosts web fonts will have nothing else. But an EOT payload is
// sfnt, decompressing woff2 means Brotli plus reversing the glyf transform,
// and a half-right font is worse than an honest substitution. Resolve names
// them in its notes instead so the gap is visible rather than silent.
var sfntExts = map[string]bool{".ttf": true, ".otf": true}

var webFontExts = map[string]bool{".woff": true, ".woff2": true}

// Resolve finds the variants of typeface, and returns what it could not.
//
// A typeface with no Regular is still returned: PowerPoint can synthesise a
// slope and a weight from one face, so three of four variants is a better
// outcome than refusing the lot.
func Resolve(ctx context.Context, typeface string, opt Options) ([]Face, []string, error) {
	name := strings.TrimSpace(typeface)
	if name == "" {
		return nil, nil, fmt.Errorf("fontsource: no typeface named")
	}
	var notes []string

	found := map[Variant]Face{}
	// web records variants seen only as a web font, and where. Judged at the
	// end rather than per directory: a .woff2 whose variant another source
	// supplies is not worth a word, and advising "ship it as .ttf" about a
	// variant the web fonts do not have either is worse than silence.
	web := map[Variant][]string{}
	for _, dir := range opt.Dirs {
		if err := scanDir(dir, found, web); err != nil {
			return nil, nil, err
		}
	}

	if opt.Runner != nil && len(found) < len(Variants()) {
		notes = fromSystem(ctx, name, opt.Runner, found)
	}

	var faces []Face
	var missing []Variant
	for _, v := range Variants() {
		if f, ok := found[v]; ok {
			faces = append(faces, f)
			continue
		}
		missing = append(missing, v)
	}
	notes = append(notes, webFontNotes(name, missing, web)...)
	return faces, append(notes, missingNote(name, missing)...), nil
}

func missingNote(name string, missing []Variant) []string {
	if len(missing) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("%s: no file found for %s", name, join(missing))}
}

// webFontNotes reports web fonts that would actually have helped: a variant
// still missing that some directory holds only as a .woff or .woff2.
//
// Scoped this narrowly because the note is advice. Fired on any unfilled
// variant it told a theme that had already shipped the right .ttf files to
// ship them again, while the variant actually missing was one its web fonts
// did not carry either.
func webFontNotes(typeface string, missing []Variant, web map[Variant][]string) []string {
	var out []string
	for _, v := range missing {
		dirs := web[v]
		if len(dirs) == 0 {
			continue
		}
		out = append(out, fmt.Sprintf(
			"%s: %s holds the %s variant only as a web font, which a .pptx cannot carry "+
				"(an EOT payload is sfnt, not woff) -- ship it as .ttf or .otf, or install the face",
			typeface, strings.Join(dirs, " and "), v))
	}
	return out
}

func join(vs []Variant) string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, string(v))
	}
	return strings.Join(out, ", ")
}

// scanDir fills in variants from one directory, keeping whatever an earlier
// directory already found.
//
// A directory that does not exist is not an error: the theme-vendored path is
// optional by design, and most themes will not have one.
func scanDir(dir string, found map[Variant]Face, web map[Variant][]string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("fontsource: read %s: %w", dir, err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		variant, ok := variantOf(name)
		if !ok {
			continue
		}
		switch {
		case webFontExts[ext]:
			web[variant] = append(web[variant], dir)
		case sfntExts[ext]:
			if _, taken := found[variant]; !taken {
				found[variant] = Face{Variant: variant, Path: filepath.Join(dir, name), Source: dir}
			}
		}
	}
	return nil
}

// variantOf reads the variant from a filename's suffix after the last dash.
//
// A file whose suffix is not a known variant is skipped rather than guessed
// at: embedding a Medium or a Light as the regular weight would put a
// typeface in the deck that the author did not choose.
func variantOf(filename string) (Variant, bool) {
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	dash := strings.LastIndex(base, "-")
	if dash < 0 {
		return "", false
	}
	v, ok := filenameSuffix[strings.ToLower(base[dash+1:])]
	return v, ok
}

// Tool is the fontconfig query the system search runs.
const Tool = "fc-match"

// fromSystem fills in the remaining variants from the installed fonts.
func fromSystem(ctx context.Context, typeface string, run Runner, found map[Variant]Face) []string {
	var notes []string
	for _, v := range Variants() {
		if _, taken := found[v]; taken {
			continue
		}
		face, note := matchOne(ctx, typeface, v, run)
		if note != "" {
			notes = append(notes, note)
		}
		if face.Path != "" {
			found[v] = face
		}
	}
	return notes
}

// matchOne asks fontconfig for one variant and checks that it got it.
//
// THE GUARD IS THE WHOLE POINT. fc-match always answers: asked for a face
// that is not installed it returns the system's best substitute rather than
// nothing, so trusting its output would embed DejaVu Sans under the name
// "Roboto" and the deck would look wrong in a way no later check could
// explain. Both the family and the style it answers with are compared against
// what was asked, and a mismatch is treated as "not installed".
func matchOne(ctx context.Context, typeface string, v Variant, run Runner) (Face, string) {
	pattern := fmt.Sprintf("%s:style=%s", typeface, fontconfigStyle[v])
	out, err := run(ctx, Tool, "-f", "%{file}\t%{family}\t%{style}", pattern)
	if err != nil {
		return Face{}, fmt.Sprintf("%s: could not ask %s for the %s variant: %v", typeface, Tool, v, err)
	}

	fields := strings.Split(strings.TrimSpace(string(out)), "\t")
	if len(fields) < 3 || fields[0] == "" {
		return Face{}, fmt.Sprintf("%s: %s gave no usable answer for the %s variant", typeface, Tool, v)
	}
	file, families, styles := fields[0], fields[1], fields[2]

	if !listContains(families, typeface) {
		// Not a note: this is the normal "the font is not installed" answer,
		// and the caller already reports the variant as missing.
		return Face{}, ""
	}
	if !listContains(styles, fontconfigStyle[v]) {
		return Face{}, ""
	}
	if ext := strings.ToLower(filepath.Ext(file)); !sfntExts[ext] {
		return Face{}, fmt.Sprintf("%s: the installed %s variant is %s, which a .pptx cannot carry",
			typeface, v, ext)
	}
	return Face{Variant: v, Path: file, Source: "installed on this machine"}, ""
}

// listContains reports whether a comma-separated fontconfig value holds want.
//
// fontconfig answers with every alias a font declares -- "Roboto,Roboto
// Medium" -- so an exact comparison against the whole string would reject a
// font that genuinely matches.
func listContains(list, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, item := range strings.Split(list, ",") {
		if strings.ToLower(strings.TrimSpace(item)) == want {
			return true
		}
	}
	return false
}
