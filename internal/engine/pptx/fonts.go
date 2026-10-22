// Typefaces: naming one, and optionally shipping it inside the package.
//
// OOXML names exactly one typeface per run. There is no fallback stack, so a
// machine without that font silently substitutes and the deck arrives in
// something else -- the failure the CSS `Roboto, sans-serif` cannot happen in
// a .pptx. Two defences, and they are different in kind:
//
//  1. Name a typeface the audience certainly has. Verifiable, costs fidelity.
//  2. Embed the font so it travels with the file. Exact, and costs trust: see
//     the note on EmbeddedFont below for what is and is not proven here.
//
// Both are available, and `Fonts` reports which applies so a build can say
// what a reader will actually see rather than leaving it to be discovered.
package pptx

import (
	"fmt"
	"strings"
)

// DefaultFont is the typeface a deck gets when nothing names one.
//
// Neutral on purpose. A consumer's font baked into a consumer-agnostic
// compiler is the same leak as the `pt-BR` notes language this package used to
// default to, and the shipped theme1.xml carried exactly that until the
// typeface became a parameter. Calibri rather than a nicer choice because it
// ships with Office, which is the only property that matters for a default:
// it cannot substitute.
const DefaultFont = "Calibri"

// fontDataExt and fontContentType are the OPC spelling of an embedded font.
const (
	fontDataExt     = "fntdata"
	fontContentType = "application/x-fontdata"
	fontRelType     = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/font"
)

// EmbeddedFont is one typeface's data, to travel inside the package.
//
// **This is unproven, and the honest state of it is worth reading before
// relying on it.**
//
// The data written here is plain sfnt -- the bytes of a .ttf or .otf.
// ECMA-376 describes the part as font data and does not mandate a container.
// PowerPoint itself writes something else: a real .pptx to hand has
// `ppt/fonts/*.fntdata` beginning with an EOT (Embedded OpenType) header --
// size, font-data size, version 0x00020002 -- wrapping MTX-compressed font
// data, with no sfnt table tags anywhere in the file.
//
// So sfnt may well not be accepted, and **it could not be tested**. The
// obvious oracle is a reader rendering a font no machine has installed, and
// that experiment was run: a font renamed to a unique family, embedded,
// converted by LibreOffice. It substituted. Then the control -- the
// real-world .pptx carrying Microsoft's own EOT fonts for a typeface not
// installed on this machine -- was converted by the same LibreOffice. It
// substituted too.
//
// **LibreOffice ignores embedded fonts in .pptx regardless of format.** That
// removes the only reader available here, so neither acceptance nor rejection
// of this format can be demonstrated, and writing an EOT encoder would be
// binary-format code with no way to tell whether it worked.
//
// Hence: embedding is off by default, and the answer a deck should actually
// rely on is naming a typeface the reader is known to have -- which `Fonts`
// reports on every build. When a licensed PowerPoint becomes available (see
// acceptance/powerpoint), testing this is the first thing worth doing with
// it.
type EmbeddedFont struct {
	Typeface string
	// Each variant is optional; a nil variant is simply not declared, and
	// PowerPoint synthesises it the way it does for any missing weight.
	Regular    []byte
	Bold       []byte
	Italic     []byte
	BoldItalic []byte
}

// variants returns the declared variants in the order OOXML lists them.
func (f EmbeddedFont) variants() []struct {
	element string
	suffix  string
	data    []byte
} {
	all := []struct {
		element string
		suffix  string
		data    []byte
	}{
		{"p:regular", "regular", f.Regular},
		{"p:bold", "bold", f.Bold},
		{"p:italic", "italic", f.Italic},
		{"p:boldItalic", "boldItalic", f.BoldItalic},
	}
	out := all[:0]
	for _, v := range all {
		if len(v.data) > 0 {
			out = append(out, v)
		}
	}
	return out
}

// FontReport says what a reader will see for one typeface.
type FontReport struct {
	Typeface string
	// Embedded is true when the package carries the font data.
	Embedded bool
	// AssumedPresent is true when the typeface is one that ships with Windows
	// or Office, so naming it without embedding is safe.
	AssumedPresent bool
}

// Safe reports whether this typeface will render as named.
func (r FontReport) Safe() bool { return r.Embedded || r.AssumedPresent }

func (r FontReport) String() string {
	switch {
	case r.Embedded:
		return fmt.Sprintf("%s: embedded in the package", r.Typeface)
	case r.AssumedPresent:
		return fmt.Sprintf("%s: ships with Windows or Office", r.Typeface)
	default:
		return fmt.Sprintf("%s: NOT embedded and not a system font — PowerPoint will substitute "+
			"something else on a machine that lacks it", r.Typeface)
	}
}

// fontsAssumedPresent are typefaces that ship with Windows or with Office, so
// naming one without embedding it cannot substitute.
//
// Deliberately short and boring. This is a promise about other people's
// machines, and every name added to it is a promise that might not hold; a
// font that merely *usually* exists belongs in the embedded set instead.
var fontsAssumedPresent = map[string]bool{
	"arial": true, "arial black": true, "calibri": true, "calibri light": true,
	"cambria": true, "candara": true, "comic sans ms": true, "consolas": true,
	"constantia": true, "corbel": true, "courier new": true, "franklin gothic medium": true,
	"gabriola": true, "georgia": true, "impact": true, "lucida console": true,
	"lucida sans unicode": true, "palatino linotype": true, "segoe ui": true,
	"segoe ui light": true, "segoe ui semibold": true, "sylfaen": true,
	"tahoma": true, "times new roman": true, "trebuchet ms": true, "verdana": true,
	"wingdings": true, "symbol": true, "webdings": true, "marlett": true,
}

// AssumedPresent reports whether naming typeface without embedding it is safe.
func AssumedPresent(typeface string) bool {
	return fontsAssumedPresent[strings.ToLower(strings.TrimSpace(typeface))]
}

// ── serialisation ───────────────────────────────────────────────────────────

// fontPartName is where one variant's data lives.
//
// The typeface is not interpolated into the path: a name is author-supplied
// and may contain anything, and a part name has to be a valid OPC path. An
// index keeps it predictable and keeps the entry-name rules in Verify happy.
func fontPartName(index int, suffix string) string {
	return fmt.Sprintf("ppt/fonts/font%d-%s.%s", index+1, suffix, fontDataExt)
}

// fontParts returns the font data parts, the relationships that reach them,
// and the <p:embeddedFontLst> that ties the two together.
//
// firstRelID is where the font relationships start; the caller owns the rest
// of presentation.xml.rels and so owns that number.
func fontParts(fonts []EmbeddedFont, firstRelID int) (parts []part, rels string, list string) {
	if len(fonts) == 0 {
		return nil, "", ""
	}

	var relBuf, listBuf strings.Builder
	rel := firstRelID

	listBuf.WriteString("\n  <p:embeddedFontLst>")
	for i, f := range fonts {
		variants := f.variants()
		if f.Typeface == "" || len(variants) == 0 {
			// Nothing to declare. Writing an empty <p:embeddedFont> would be a
			// font entry naming no data, which is exactly what Verify's
			// font-embedding rule exists to catch.
			continue
		}
		fmt.Fprintf(&listBuf, `<p:embeddedFont><p:font typeface="%s"/>`, escape(f.Typeface))
		for _, v := range variants {
			name := fontPartName(i, v.suffix)
			parts = append(parts, part{name: name, body: v.data})
			fmt.Fprintf(&relBuf,
				`  <Relationship Id="rId%d" Type="%s" Target="%s"/>`+"\n",
				rel, fontRelType, strings.TrimPrefix(name, "ppt/"))
			fmt.Fprintf(&listBuf, `<%s r:id="rId%d"/>`, v.element, rel)
			rel++
		}
		listBuf.WriteString(`</p:embeddedFont>`)
	}
	listBuf.WriteString("</p:embeddedFontLst>")

	if len(parts) == 0 {
		return nil, "", ""
	}
	return parts, relBuf.String(), listBuf.String()
}

// fontScheme renders the theme's major and minor latin typeface.
//
// Both are set to the same face: a deck has one typeface, and a theme that
// wanted a display/body pair would be describing something this compiler does
// not model.
func resolveFont(name string) string {
	if strings.TrimSpace(name) == "" {
		return DefaultFont
	}
	return strings.TrimSpace(name)
}

// latinTypefacePlaceholder is what the shipped theme1.xml carries where a
// typeface belongs.
const latinTypefacePlaceholder = "__LATIN_TYPEFACE__"

// themeXML is the theme part with the deck's typeface substituted into its
// fontScheme, for both the major and minor face.
//
// The asset is a template rather than a finished file because the fontScheme
// is the one thing in it that belongs to the consumer, and a compiler that
// hardcodes a consumer's font is not consumer-agnostic. The placeholder is
// required: if a future edit to the asset drops it, this fails loudly instead
// of shipping whatever font the asset happened to name.
func themeXML(font string) ([]byte, error) {
	raw, err := assets.ReadFile("assets/theme1.xml")
	if err != nil {
		return nil, fmt.Errorf("pptx: read embedded theme: %w", err)
	}
	body := string(raw)
	if !strings.Contains(body, latinTypefacePlaceholder) {
		return nil, fmt.Errorf("pptx: assets/theme1.xml has no %s to substitute, so its "+
			"fontScheme would ship a hardcoded typeface", latinTypefacePlaceholder)
	}
	return []byte(strings.ReplaceAll(body, latinTypefacePlaceholder, escape(resolveFont(font)))), nil
}
