// Package pptx writes a PowerPoint file whose every slide is one full-bleed
// image, with the slide's text preserved in the speaker notes.
//
// This is "image mode", and the tradeoff is deliberate: visual fidelity is
// exact because the slide *is* the render, and nothing is editable because
// there are no native shapes. The editable artefact is the deck source. Notes
// are what keep the words recoverable — searchable in PowerPoint, readable by a
// screen reader, and diffable by anyone who needs the content back.
//
// A .pptx is a zip of OOXML parts. Everything static lives in assets/; the
// per-slide parts and the manifests are generated here.
package pptx

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"embed"
	"encoding/xml"
	"fmt"
	"hash/crc32"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

//go:embed assets/*
var assets embed.FS

// EMUPerInch is the English Metric Unit, OOXML's internal length.
const EMUPerInch = 914400

// Slide is one page of the deck.
type Slide struct {
	// PNG is the rendered image; it fills the whole slide. Used when Shapes is
	// empty, which is image mode and also the per-slide fallback for a layout
	// the theme has not described in PPTX terms.
	PNG []byte
	// Notes is the slide's text, preserved as speaker notes.
	Notes string
	// Shapes, when non-empty, makes this slide native: real text boxes,
	// tables and pictures instead of one flat image. PNG is then ignored.
	//
	// This package deliberately knows nothing about layouts or themes -- it
	// renders the shapes it is handed. Deciding which shapes a slide gets, or
	// that it gets none and falls back to its image, belongs to the caller.
	Shapes []Shape
}

// Native reports whether this slide is editable rather than a flat image.
func (s Slide) Native() bool { return len(s.Shapes) > 0 }

// Deck is what Write serialises.
type Deck struct {
	WidthIn  float64
	HeightIn float64
	// Lang is the document's BCP 47 language tag, stamped on every speaker-notes
	// run. Empty falls back to DefaultNotesLang.
	Lang string
	// Font is the typeface the theme's fontScheme declares. Empty falls back
	// to DefaultFont, which is deliberately a font Office ships: OOXML has no
	// fallback stack, so an absent typeface substitutes silently.
	Font string
	// EmbeddedFonts travel inside the package so the typeface cannot
	// substitute. Optional, and see EmbeddedFont for what is proven.
	EmbeddedFonts []EmbeddedFont
	Slides        []Slide
}

// DefaultNotesLang is the language tag used when a deck names none. It only
// sets PowerPoint's spell-check language for the notes pane, so the fallback
// is cosmetic — but it must be a real tag, because the zero value would write
// lang="" into the XML.
//
// There is deliberately no consumer's language here: a hardcoded pt-BR was
// one deck repo's language baked into a public, consumer-agnostic compiler,
// which is exactly what the -deck-root/-theme-root discipline exists to
// avoid. The language belongs to the content.
const DefaultNotesLang = "en"

// Write serialises the deck to a .pptx at dst.
//
// The write is atomic: the archive is built in a temporary file beside dst
// and only moved into place with os.Rename once it is complete. A failure
// partway through — or at the final rename — removes the temporary file and
// never touches dst, so a consumer never sees a truncated .pptx where a real
// one used to be. The temp file is created in dst's own directory because
// os.Rename fails across filesystems.
func Write(dst string, d Deck) error {
	if len(d.Slides) == 0 {
		return fmt.Errorf("pptx: deck has no slides")
	}
	if d.WidthIn <= 0 || d.HeightIn <= 0 {
		return fmt.Errorf("pptx: deck has no size")
	}

	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return fmt.Errorf("pptx: create temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	z := zip.NewWriter(tmp)
	if err := writeParts(z, d); err != nil {
		_ = z.Close()
		_ = tmp.Close()
		return err
	}
	if err := z.Close(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("pptx: close archive: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("pptx: close %s: %w", tmpPath, err)
	}

	// Verify before the rename, never after: the temp file is still the only
	// copy, so a package that fails is removed by the deferred cleanup and the
	// destination is never touched. Four defects shipped from here, each one
	// accepted by every reader this repo had; an opt-in check would not have
	// caught any of them, because the gap was never in the writing.
	findings, err := Verify(tmpPath)
	if err != nil {
		return fmt.Errorf("pptx: verify the package just written: %w", err)
	}
	if len(findings) > 0 {
		return &FindingsError{Path: dst, Findings: findings}
	}

	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("pptx: rename into place at %s: %w", dst, err)
	}
	committed = true
	return nil
}

// part is one entry in the package. Order matters twice over: OPC readers
// expect [Content_Types].xml first, and a fixed order makes the output
// reproducible — the same deck produces a byte-identical file.
type part struct {
	name string
	body []byte
}

func writeParts(z *zip.Writer, d Deck) error {
	parts := []part{{name: "[Content_Types].xml",
		body: []byte(contentTypes(len(d.Slides), len(d.EmbeddedFonts) > 0))}}

	static := []struct{ name, src string }{
		{"_rels/.rels", "assets/root.rels.xml"},
		// theme1.xml is read below rather than here: its fontScheme carries
		// the deck's typeface, and the shipped asset used to hardcode one
		// consumer's font -- the same leak as this package's old pt-BR notes
		// default, in a compiler whose whole discipline is naming no consumer.
		// The notes master gets its OWN theme part, from the same asset bytes.
		//
		// ECMA-376 allocates a theme "per Handout Master, Notes Master, Slide
		// Master or Presentation part", and PowerPoint enforces it for the notes
		// master: pointing both masters at theme1.xml opens in python-pptx and
		// LibreOffice without complaint and makes a desktop PowerPoint refuse the
		// whole document with a generic "problem with the content" repair prompt.
		// Repairing such a file is what revealed it -- PowerPoint wrote out a new
		// theme2.xml and repointed the notes master at it, the only change it made
		// to the package graph rather than to optional elements.
		//
		// Sharing is not banned in general: PowerPoint's own output has the
		// presentation part and the slide master both on theme1, which is why
		// presentation.xml.rels below still targets it. It is the notes master
		// that needs its own.
		//
		{"ppt/slideMasters/slideMaster1.xml", "assets/slideMaster1.xml"},
		{"ppt/slideMasters/_rels/slideMaster1.xml.rels", "assets/slideMaster1.xml.rels"},
		{"ppt/slideLayouts/slideLayout1.xml", "assets/slideLayout1.xml"},
		{"ppt/slideLayouts/_rels/slideLayout1.xml.rels", "assets/slideLayout1.xml.rels"},
		{"ppt/notesMasters/notesMaster1.xml", "assets/notesMaster1.xml"},
		{"ppt/notesMasters/_rels/notesMaster1.xml.rels", "assets/notesMaster1.xml.rels"},
		// Real PowerPoint files always carry these. python-pptx opens a package
		// without them; LibreOffice's filter refuses it outright, with only
		// "source file could not be loaded" to go on.
		{"ppt/presProps.xml", "assets/presProps.xml"},
		{"ppt/viewProps.xml", "assets/viewProps.xml"},
		{"ppt/tableStyles.xml", "assets/tableStyles.xml"},
		{"docProps/core.xml", "assets/core.xml"},
		{"docProps/app.xml", "assets/app.xml"},
	}
	for _, st := range static {
		body, err := assets.ReadFile(st.src)
		if err != nil {
			return fmt.Errorf("pptx: read embedded %s: %w", st.src, err)
		}
		parts = append(parts, part{name: st.name, body: body})
	}

	theme, err := themeXML(d.Font)
	if err != nil {
		return err
	}
	parts = append(parts,
		part{"ppt/theme/theme1.xml", theme},
		// The notes master needs its own theme part; see the note above the
		// slide master's relationships for why sharing one is refused.
		part{"ppt/theme/theme2.xml", theme},
	)

	fontBodies, fontRels, fontList, err := fontParts(d.EmbeddedFonts, presRels{slides: len(d.Slides)}.firstFont())
	if err != nil {
		return err
	}
	parts = append(parts,
		part{"ppt/presentation.xml", []byte(presentation(d, fontList))},
		part{"ppt/_rels/presentation.xml.rels", []byte(presentationRels(len(d.Slides), fontRels))},
	)
	parts = append(parts, fontBodies...)

	for i, s := range d.Slides {
		n := i + 1
		images := slideImages(s)
		for j, body := range images {
			parts = append(parts, part{mediaName(n, j, s.Native()), body})
		}
		parts = append(parts,
			part{fmt.Sprintf("ppt/slides/slide%d.xml", n), []byte(slideXML(n, d))},
			part{fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", n), []byte(slideRels(n, len(images), s.Native()))},
			part{fmt.Sprintf("ppt/notesSlides/notesSlide%d.xml", n), []byte(notesXML(s.Notes, d.Lang))},
			part{fmt.Sprintf("ppt/notesSlides/_rels/notesSlide%d.xml.rels", n), []byte(notesRels(n))},
		)
	}

	for _, pt := range parts {
		if err := add(z, pt.name, pt.body); err != nil {
			return err
		}
	}
	return nil
}

// zipEpoch is a fixed, valid DOS timestamp.
//
// A constant keeps the output reproducible: the same deck produces a
// byte-identical .pptx. It exists on FileHeader.Modified mainly for
// readability — see zipModDate/zipModTime below for why Modified alone does
// not reach the file.
var zipEpoch = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)

// zipModDate and zipModTime are zipEpoch, pre-encoded as the legacy MS-DOS
// date/time pair the local and central-directory headers actually carry —
// 1980-01-01 00:00, packed per the DOS format (year-1980<<9 | month<<5 |
// day, hour<<11 | minute<<5 | second/2). Every part in this package silently
// carried 0x0000/0x0000 — month 0, day 0, not a real date — until this was
// added: Writer.CreateRaw (below) never converts FileHeader.Modified to
// these fields the way CreateHeader does; that conversion lives entirely
// inside CreateHeader, which this package deliberately does not call. A
// zero DOS date is valid enough for python-pptx and LibreOffice to open
// without complaint, but not for the stricter OPC/ZIP reader real
// PowerPoint is built on, which is the one client this package cannot
// afford to get wrong.
const (
	zipModDate uint16 = 1<<5 | 1 // month 1, day 1, year offset 0
	zipModTime uint16 = 0
)

// zipVersion is "2.0", the version both the local and central-directory
// headers declare for "version needed to extract" (and, in the central
// directory, the low byte of "version made by") — the minimum version a
// reader needs to know it can Deflate-decompress an entry.
//
// CreateHeader derives this unconditionally: `fh.ReaderVersion =
// zipVersion20` and `fh.CreatorVersion = fh.CreatorVersion&0xff00 |
// zipVersion20`. CreateRaw sets neither, so every entry in this package
// silently declared version 0.0 — not a real ZIP version, just the Go zero
// value — until this was added. python-pptx, LibreOffice and modern
// PowerPoint read a declared version of zero without complaint; the OPC
// parser in the Office install this shipped to on Windows 7 does not, and
// rejected the whole package with a generic "problema com o conteúdo"
// repair prompt — the same symptom as the zero DOS date above, caused the
// same way, in a field none of this package's other verification tools
// (zipfile, unzip, LibreOffice, python-pptx) check.
const zipVersion uint16 = 20

// add writes one part, with its CRC and sizes in the local header.
//
// This is the subtle part. archive/zip's ordinary Create/CreateHeader path
// streams the entry and appends a *data descriptor*, setting general-purpose
// bit 3 and leaving CRC and sizes as zero in the local header. That is valid
// ZIP — python-pptx reads such a file happily — but LibreOffice's package
// reader wants the sizes up front and rejects the whole document with nothing
// but "source file could not be loaded". Compressing ahead of time and writing
// through CreateRaw keeps bit 3 clear.
//
// Images are stored rather than deflated: PNG is already compressed, so
// deflating it again costs time and saves nothing.
func add(z *zip.Writer, name string, body []byte) error {
	header := &zip.FileHeader{
		Name:           name,
		Modified:       zipEpoch,
		ModifiedDate:   zipModDate,
		ModifiedTime:   zipModTime,
		CreatorVersion: zipVersion,
		ReaderVersion:  zipVersion,
		Method:         zip.Deflate,
	}
	payload := body

	if path.Ext(name) == ".png" {
		header.Method = zip.Store
	} else {
		var buf bytes.Buffer
		fw, err := flate.NewWriter(&buf, flate.DefaultCompression)
		if err != nil {
			return fmt.Errorf("pptx: compress %s: %w", name, err)
		}
		if _, err := fw.Write(body); err != nil {
			return fmt.Errorf("pptx: compress %s: %w", name, err)
		}
		if err := fw.Close(); err != nil {
			return fmt.Errorf("pptx: compress %s: %w", name, err)
		}
		payload = buf.Bytes()
	}

	header.CRC32 = crc32.ChecksumIEEE(body)
	header.UncompressedSize64 = uint64(len(body))
	header.CompressedSize64 = uint64(len(payload))

	w, err := z.CreateRaw(header)
	if err != nil {
		return fmt.Errorf("pptx: create %s: %w", name, err)
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("pptx: write %s: %w", name, err)
	}
	return nil
}

func contentTypes(slides int, embedsFonts bool) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Default Extension="png" ContentType="image/png"/>
__FONT_DEFAULT__  <Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/>
  <Override PartName="/ppt/slideMasters/slideMaster1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"/>
  <Override PartName="/ppt/slideLayouts/slideLayout1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"/>
  <Override PartName="/ppt/notesMasters/notesMaster1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.notesMaster+xml"/>
  <Override PartName="/ppt/theme/theme1.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/>
  <Override PartName="/ppt/theme/theme2.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/>
  <Override PartName="/ppt/presProps.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presProps+xml"/>
  <Override PartName="/ppt/viewProps.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.viewProps+xml"/>
  <Override PartName="/ppt/tableStyles.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.tableStyles+xml"/>
  <Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>
  <Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>
`)
	for n := 1; n <= slides; n++ {
		fmt.Fprintf(&b, `  <Override PartName="/ppt/slides/slide%d.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>`+"\n", n)
		fmt.Fprintf(&b, `  <Override PartName="/ppt/notesSlides/notesSlide%d.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.notesSlide+xml"/>`+"\n", n)
	}
	b.WriteString("</Types>\n")

	// Declared only when a font part exists. A Default for an extension no
	// part uses is valid OPC but it is a claim about a package that is not
	// true, and this file is read by people deciding what the package holds.
	fontDefault := ""
	if embedsFonts {
		fontDefault = fmt.Sprintf("  <Default Extension=%q ContentType=%q/>\n", fontDataExt, fontContentType)
	}
	return strings.Replace(b.String(), "__FONT_DEFAULT__", fontDefault, 1)
}

// presRels names the relationships of ppt/presentation.xml.
//
// These were inline arithmetic (slides+2, slides+3, ...) in three places that
// had to agree, and fonts needed a fourth. Naming them makes the agreement
// checkable rather than remembered; Verify's relationship-id rule catches it
// if they ever stop agreeing.
type presRels struct{ slides int }

func (p presRels) slideMaster() int { return 1 }
func (p presRels) slide(n int) int  { return n + 1 }
func (p presRels) notesMaster() int { return p.slides + 2 }
func (p presRels) theme() int       { return p.slides + 3 }
func (p presRels) presProps() int   { return p.slides + 4 }
func (p presRels) viewProps() int   { return p.slides + 5 }
func (p presRels) tableStyles() int { return p.slides + 6 }
func (p presRels) firstFont() int   { return p.slides + 7 }

func presentation(d Deck, fontList string) string {
	r := presRels{slides: len(d.Slides)}
	var ids strings.Builder
	for n := 1; n <= len(d.Slides); n++ {
		// Slide ids must be >= 256; rIds start after the master.
		fmt.Fprintf(&ids, `<p:sldId id="%d" r:id="rId%d"/>`, 255+n, r.slide(n))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:presentation xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
                xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"
                xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">
  <p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId%d"/></p:sldMasterIdLst>
  <p:notesMasterIdLst><p:notesMasterId r:id="rId%d"/></p:notesMasterIdLst>
  <p:sldIdLst>%s</p:sldIdLst>
  <p:sldSz cx="%d" cy="%d"/>
  <p:notesSz cx="%d" cy="%d"/>%s
</p:presentation>
`, r.slideMaster(), r.notesMaster(), ids.String(),
		emu(d.WidthIn), emu(d.HeightIn), emu(d.HeightIn), emu(d.WidthIn), fontList)
}

func presentationRels(slides int, fontRels string) string {
	r := presRels{slides: slides}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
`)
	fmt.Fprintf(&b, `  <Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="slideMasters/slideMaster1.xml"/>`+"\n", r.slideMaster())
	for n := 1; n <= slides; n++ {
		fmt.Fprintf(&b, `  <Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide%d.xml"/>`+"\n", r.slide(n), n)
	}
	for _, fixed := range []struct {
		id     int
		typ    string
		target string
	}{
		{r.notesMaster(), "notesMaster", "notesMasters/notesMaster1.xml"},
		{r.theme(), "theme", "theme/theme1.xml"},
		{r.presProps(), "presProps", "presProps.xml"},
		{r.viewProps(), "viewProps", "viewProps.xml"},
		{r.tableStyles(), "tableStyles", "tableStyles.xml"},
	} {
		fmt.Fprintf(&b, `  <Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/%s" Target="%s"/>`+"\n",
			fixed.id, fixed.typ, fixed.target)
	}
	b.WriteString(fontRels)
	b.WriteString("</Relationships>\n")
	return b.String()
}

// slideImages returns the image bodies this slide contributes, in the order
// their relationships are assigned.
func slideImages(s Slide) [][]byte {
	if !s.Native() {
		return [][]byte{s.PNG}
	}
	var out [][]byte
	for _, sh := range s.Shapes {
		if sh.Kind == ShapePicture {
			out = append(out, sh.PNG)
		}
	}
	return out
}

// mediaName keeps image mode's part names unchanged -- ppt/media/slideN.png --
// while a native slide, which may carry several pictures or none, numbers them.
func mediaName(slide, index int, native bool) string {
	if !native {
		return fmt.Sprintf("ppt/media/slide%d.png", slide)
	}
	return fmt.Sprintf("ppt/media/slide%d-%d.png", slide, index+1)
}

// Relationship ids within a slide. Images start after the fixed two so the
// same convention serves a slide with one picture and a slide with six; a
// dangling r:id is one of the things Verify checks, which is what makes a
// single convention safe to rely on here.
const (
	relSlideLayout = "rId1"
	relNotesSlide  = "rId2"
	relFirstImage  = 3
)

func imageRelID(index int) string { return fmt.Sprintf("rId%d", relFirstImage+index) }

func slideXML(n int, d Deck) string {
	s := d.Slides[n-1]
	body := fullBleedPicture(n, d)
	if s.Native() {
		body = shapeTree(s, d.Lang)
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
       xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"
       xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">
  <p:cSld>
    <p:spTree>
      <p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>
      <p:grpSpPr>
        <a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/>
          <a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm>
      </p:grpSpPr>%s
    </p:spTree>
  </p:cSld>
  <p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr>
</p:sld>
`, body)
}

// fullBleedPicture is image mode: the whole slide is one picture.
func fullBleedPicture(n int, d Deck) string {
	return fmt.Sprintf(`
      <p:pic>
        <p:nvPicPr>
          <p:cNvPr id="%d" name="Slide %d"/>
          <p:cNvPicPr><a:picLocks noChangeAspect="1"/></p:cNvPicPr>
          <p:nvPr/>
        </p:nvPicPr>
        <p:blipFill>
          <a:blip r:embed="%s"/>
          <a:stretch><a:fillRect/></a:stretch>
        </p:blipFill>
        <p:spPr>
          <a:xfrm><a:off x="0" y="0"/><a:ext cx="%d" cy="%d"/></a:xfrm>
          <a:prstGeom prst="rect"><a:avLst/></a:prstGeom>
        </p:spPr>
      </p:pic>`, n+1, n, imageRelID(0), emu(d.WidthIn), emu(d.HeightIn))
}

// shapeTree is native mode. Shape ids start at 2 because the group shape that
// wraps every slide tree is id 1.
func shapeTree(s Slide, lang string) string {
	var b strings.Builder
	id := 2
	picture := 0
	for _, sh := range s.Shapes {
		switch sh.Kind {
		case ShapeText:
			b.WriteString(textBoxXML(id, sh, lang))
		case ShapeBullets:
			b.WriteString(bulletsXML(id, sh, lang))
		case ShapeTable:
			b.WriteString(tableXML(id, sh, lang))
		case ShapeRect:
			b.WriteString(rectXML(id, sh))
		case ShapePicture:
			b.WriteString(pictureXML(id, sh, imageRelID(picture)))
			picture++
		default:
			// An unknown kind is dropped rather than guessed at: a shape this
			// package cannot render is a caller bug, and emitting something
			// plausible would hide it behind a slide that merely looks wrong.
			continue
		}
		id++
	}
	return b.String()
}

func slideRels(n, images int, native bool) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
`)
	fmt.Fprintf(&b, `  <Relationship Id="%s" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/>`+"\n", relSlideLayout)
	fmt.Fprintf(&b, `  <Relationship Id="%s" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesSlide" Target="../notesSlides/notesSlide%d.xml"/>`+"\n", relNotesSlide, n)
	for j := 0; j < images; j++ {
		fmt.Fprintf(&b, `  <Relationship Id="%s" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="../media/%s"/>`+"\n",
			imageRelID(j), strings.TrimPrefix(mediaName(n, j, native), "ppt/media/"))
	}
	b.WriteString("</Relationships>\n")
	return b.String()
}

func notesXML(notes, lang string) string {
	if strings.TrimSpace(lang) == "" {
		lang = DefaultNotesLang
	}
	var paras strings.Builder
	lines := strings.Split(strings.TrimSpace(notes), "\n")
	for _, line := range lines {
		fmt.Fprintf(&paras, `<a:p><a:r><a:rPr lang="%s" dirty="0"/><a:t>%s</a:t></a:r></a:p>`,
			escape(lang), escape(line))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:notes xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
         xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"
         xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">
  <p:cSld>
    <p:spTree>
      <p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>
      <p:grpSpPr>
        <a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/>
          <a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm>
      </p:grpSpPr>
      <p:sp>
        <p:nvSpPr>
          <p:cNvPr id="2" name="Notes Placeholder"/>
          <p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr>
          <p:nvPr><p:ph type="body" idx="1"/></p:nvPr>
        </p:nvSpPr>
        <p:spPr/>
        <p:txBody><a:bodyPr/><a:lstStyle/>%s</p:txBody>
      </p:sp>
    </p:spTree>
  </p:cSld>
  <p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr>
</p:notes>
`, paras.String())
}

func notesRels(n int) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesMaster" Target="../notesMasters/notesMaster1.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="../slides/slide%d.xml"/>
</Relationships>
`, n)
}

func emu(inches float64) int64 { return int64(inches*EMUPerInch + 0.5) }

// escape makes text safe to embed in XML content.
func escape(s string) string {
	var b strings.Builder
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		// EscapeText only fails if the writer fails; a strings.Builder cannot.
		return ""
	}
	return b.String()
}
