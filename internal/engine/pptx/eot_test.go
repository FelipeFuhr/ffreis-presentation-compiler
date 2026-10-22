package pptx_test

import (
	"encoding/binary"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine/pptx"
)

// ── a real, if minimal, font ────────────────────────────────────────────────

// fontSpec is what buildSfnt should produce.
type fontSpec struct {
	Family, Style string
	Weight        uint16
	Italic        bool
	FsType        uint16
	OS2Version    uint16
}

// buildSfnt assembles a valid sfnt carrying the three tables an EOT header is
// built from.
//
// Synthetic rather than a checked-in .ttf: the writer reads exactly these
// fields, so a font constructed to state them is a sharper fixture than a
// real face would be -- a test can ask for weight 700 italic with restricted
// embedding bits and get precisely that, which no shipped font obliges. It
// also keeps a binary blob and a font licence out of the repo.
func buildSfnt(t *testing.T, spec fontSpec) []byte {
	t.Helper()
	os2 := make([]byte, 86)
	binary.BigEndian.PutUint16(os2[0:], spec.OS2Version)
	binary.BigEndian.PutUint16(os2[4:], spec.Weight)
	binary.BigEndian.PutUint16(os2[8:], spec.FsType)
	for i := 0; i < 10; i++ {
		os2[32+i] = byte(i + 1) // a PANOSE the test can recognise
	}
	binary.BigEndian.PutUint32(os2[42:], 0x00000003) // UnicodeRange1
	if spec.Italic {
		binary.BigEndian.PutUint16(os2[62:], 1) // fsSelection ITALIC
	}
	binary.BigEndian.PutUint32(os2[78:], 0x00000001) // CodePageRange1

	head := make([]byte, 54)
	binary.BigEndian.PutUint32(head[8:], 0xDEADBEEF)  // checkSumAdjustment
	binary.BigEndian.PutUint32(head[12:], 0x5F0F3CF5) // the head magic
	if spec.Italic {
		binary.BigEndian.PutUint16(head[44:], 2) // macStyle ITALIC
	}

	return assemble(t, map[string][]byte{
		"OS/2": os2,
		"head": head,
		"name": nameTable(spec),
	})
}

// nameTable builds a Windows/UCS-2 name table with the four strings EOT uses.
func nameTable(spec fontSpec) []byte {
	entries := []struct {
		id  uint16
		val string
	}{
		{1, spec.Family}, {2, spec.Style},
		{4, spec.Family + " " + spec.Style}, {5, "Version 1.000"},
	}
	storage := 6 + len(entries)*12

	head := make([]byte, 0, storage)
	head = binary.BigEndian.AppendUint16(head, 0)                    // format
	head = binary.BigEndian.AppendUint16(head, uint16(len(entries))) // count
	head = binary.BigEndian.AppendUint16(head, uint16(storage))      // stringOffset

	var strs []byte
	for _, e := range entries {
		encoded := utf16BE(e.val)
		head = binary.BigEndian.AppendUint16(head, 3)      // Windows
		head = binary.BigEndian.AppendUint16(head, 1)      // UCS-2
		head = binary.BigEndian.AppendUint16(head, 0x0409) // en-US
		head = binary.BigEndian.AppendUint16(head, e.id)
		head = binary.BigEndian.AppendUint16(head, uint16(len(encoded)))
		head = binary.BigEndian.AppendUint16(head, uint16(len(strs)))
		strs = append(strs, encoded...)
	}
	return append(head, strs...)
}

func utf16BE(s string) []byte {
	var out []byte
	for _, u := range utf16.Encode([]rune(s)) {
		out = binary.BigEndian.AppendUint16(out, u)
	}
	return out
}

// assemble writes an sfnt offset table and directory around the given tables.
//
// The directory must be sorted by tag, which "OS/2" < "head" < "name" already
// is in ASCII; stated here so a future table added out of order is noticed.
func assemble(t *testing.T, tables map[string][]byte) []byte {
	t.Helper()
	order := []string{"OS/2", "head", "name"}
	if len(tables) != len(order) {
		t.Fatalf("assemble knows %d tables, got %d", len(order), len(tables))
	}

	offset := 12 + len(order)*16
	out := make([]byte, 0, offset)
	out = binary.BigEndian.AppendUint32(out, 0x00010000) // TrueType
	out = binary.BigEndian.AppendUint16(out, uint16(len(order)))
	out = binary.BigEndian.AppendUint16(out, 0) // searchRange, unread here
	out = binary.BigEndian.AppendUint16(out, 0) // entrySelector
	out = binary.BigEndian.AppendUint16(out, 0) // rangeShift

	var bodies []byte
	for _, tag := range order {
		body := tables[tag]
		out = append(out, tag...)
		out = binary.BigEndian.AppendUint32(out, 0) // checkSum, unread here
		out = binary.BigEndian.AppendUint32(out, uint32(offset+len(bodies)))
		out = binary.BigEndian.AppendUint32(out, uint32(len(body)))
		bodies = append(bodies, body...)
	}
	return append(out, bodies...)
}

// ── what the container must say ─────────────────────────────────────────────

// fntdataOf writes a deck embedding one font and returns the .fntdata part.
func fntdataOf(t *testing.T, f pptx.EmbeddedFont) []byte {
	t.Helper()
	_, parts := writeDeckTo(t, fontDeck(f))
	for name, body := range parts {
		if strings.HasSuffix(name, ".fntdata") {
			return []byte(body)
		}
	}
	t.Fatal("no .fntdata part was written")
	return nil
}

// writeFontDeck writes a deck embedding f and returns only whether it was
// accepted, which is all the licence and malformed-font tests ask.
func writeFontDeck(t *testing.T, f pptx.EmbeddedFont) error {
	t.Helper()
	return pptx.Write(filepath.Join(t.TempDir(), "deck.pptx"), fontDeck(f))
}

func TestEmbeddedFontIsAnEOTContainerNotRawSfnt(t *testing.T) {
	// The writer used to emit the .ttf bytes with no header at all, so a
	// reader had no magic number to recognise and no family name, weight or
	// italic flag to match against the typeface the slides name. A real
	// PowerPoint-authored file settled the format; these are its fields.
	raw := fntdataOf(t, pptx.EmbeddedFont{
		Typeface: "ZZProbeFace",
		Regular:  buildSfnt(t, fontSpec{Family: "ZZProbeFace", Style: "Regular", Weight: 400, OS2Version: 1}),
	})

	if got := binary.LittleEndian.Uint16(raw[0x22:]); got != 0x504C {
		t.Errorf("MagicNumber = %#04x, want the EOT %#04x", got, 0x504C)
	}
	if got := binary.LittleEndian.Uint32(raw[8:]); got != 0x00020002 {
		t.Errorf("Version = %#08x, want %#08x as PowerPoint writes", got, 0x00020002)
	}
	// EOTSize counts everything; FontDataSize counts only what follows the
	// header. Confirmed against PowerPoint's own file, where the difference
	// was exactly the header length.
	if got := binary.LittleEndian.Uint32(raw[0:]); int(got) != len(raw) {
		t.Errorf("EOTSize = %d, want the part's own length %d", got, len(raw))
	}
	if hdr := len(raw) - int(binary.LittleEndian.Uint32(raw[4:])); hdr <= 0 || hdr >= len(raw) {
		t.Errorf("FontDataSize implies a %d-byte header in a %d-byte part", hdr, len(raw))
	}
}

func TestEmbeddedFontCarriesTheFacesOwnWeightAndSlope(t *testing.T) {
	// These are what a reader matches the embedded data against, so getting
	// them from the font rather than from the variant name is the point: a
	// file called Roboto-bold.ttf that is not bold would otherwise be
	// declared bold.
	for _, tc := range []struct {
		name   string
		spec   fontSpec
		weight uint32
		italic bool
	}{
		{"regular", fontSpec{Family: "F", Style: "Regular", Weight: 400, OS2Version: 1}, 400, false},
		{"bold", fontSpec{Family: "F", Style: "Bold", Weight: 700, OS2Version: 1}, 700, false},
		{"italic", fontSpec{Family: "F", Style: "Italic", Weight: 400, Italic: true, OS2Version: 1}, 400, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := fntdataOf(t, pptx.EmbeddedFont{Typeface: "F", Regular: buildSfnt(t, tc.spec)})
			if got := binary.LittleEndian.Uint32(raw[0x1C:]); got != tc.weight {
				t.Errorf("Weight = %d, want %d", got, tc.weight)
			}
			if got := raw[0x1B] != 0; got != tc.italic {
				t.Errorf("Italic = %v, want %v", got, tc.italic)
			}
		})
	}
}

func TestEmbeddingIsRefusedWhenTheLicenceForbidsIt(t *testing.T) {
	// fsType is a licence, not a hint. Embedding a face whose vendor forbids
	// it would put a licence violation in the output, so this fails the build
	// rather than warning.
	for _, tc := range []struct {
		name   string
		fsType uint16
	}{
		{"restricted", 0x0002},
		{"bitmap only", 0x0200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := writeFontDeck(t, pptx.EmbeddedFont{
				Typeface: "F",
				Regular:  buildSfnt(t, fontSpec{Family: "F", Style: "Regular", Weight: 400, FsType: tc.fsType, OS2Version: 1}),
			})
			if err == nil {
				t.Fatalf("a font with fsType %#04x was embedded anyway", tc.fsType)
			}
			if !errors.Is(err, pptx.ErrEmbeddingRestricted) {
				t.Errorf("error does not identify the licence as the reason: %v", err)
			}
		})
	}
}

func TestEmbeddingIsAllowedForThePermissiveFsTypeBits(t *testing.T) {
	// "Preview & Print" and "Editable" both permit what this does; treating
	// any non-zero fsType as a refusal would reject most commercial faces
	// that explicitly allow embedding.
	for _, fsType := range []uint16{0x0000, 0x0004, 0x0008} {
		if err := writeFontDeck(t, pptx.EmbeddedFont{
			Typeface: "F",
			Regular:  buildSfnt(t, fontSpec{Family: "F", Style: "Regular", Weight: 400, FsType: fsType, OS2Version: 1}),
		}); err != nil {
			t.Errorf("fsType %#04x permits embedding but was refused: %v", fsType, err)
		}
	}
}

func TestEmbeddingRejectsSomethingThatIsNotAFont(t *testing.T) {
	// The fixture this replaced was fifteen bytes of the words "probe font
	// data" behind a TrueType magic, and the old writer embedded it happily:
	// nothing read the bytes, so nothing could tell.
	// Note "empty" is absent: nil data is no variant, which is dropped rather
	// than rejected, and TestAnEmbeddedFontWithNoDataIsNotDeclared covers it.
	// These are bytes that CLAIM to be a font and are not.
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"too short", []byte{0x00, 0x01, 0x00, 0x00}},
		{"wrong magic", append([]byte("RIFF"), make([]byte, 64)...)},
		{"truncated directory", append([]byte{0x00, 0x01, 0x00, 0x00, 0x00, 0x09}, make([]byte, 8)...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := writeFontDeck(t, pptx.EmbeddedFont{Typeface: "F", Regular: tc.body}); err == nil {
				t.Error("embedded without complaint")
			}
		})
	}
}

func TestOS2VersionZeroIsNotAnError(t *testing.T) {
	// Code-page ranges arrived in OS/2 version 1. An older table is not
	// malformed; the EOT fields are simply left zero, which is accurate.
	if err := writeFontDeck(t, pptx.EmbeddedFont{
		Typeface: "F",
		Regular:  buildSfnt(t, fontSpec{Family: "F", Style: "Regular", Weight: 400, OS2Version: 0}),
	}); err != nil {
		t.Errorf("an OS/2 version 0 font was refused: %v", err)
	}
}

func TestVerifyCatchesRawSfntWhereAnEOTContainerBelongs(t *testing.T) {
	// The regression test for the defect this file exists to fix. A package
	// carrying the .ttf bytes directly is well-formed OOXML pointing at data
	// no reader can identify, and every other rule passes on it.
	font := probeSfnt(t)
	parts := explode(t, mustBytes(t, fontDeck(pptx.EmbeddedFont{Typeface: "ZZProbeFace", Regular: font})))
	for i := range parts {
		if parts[i].name == "ppt/fonts/font1-regular.fntdata" {
			parts[i].body = font
		}
	}
	findings := verifyBytes(t, rezip(t, parts))
	if rules(findings)[pptx.RuleFontContainer] == 0 {
		t.Errorf("raw sfnt in a font part was not reported, got %v", rules(findings))
	}
}

func TestVerifyCatchesATruncatedFontPart(t *testing.T) {
	// A header promising more font data than the part holds is what a copy
	// truncated in transit looks like, and the bytes still unzip cleanly.
	parts := explode(t, mustBytes(t, fontDeck(pptx.EmbeddedFont{
		Typeface: "ZZProbeFace", Regular: probeSfnt(t),
	})))
	for i := range parts {
		if parts[i].name == "ppt/fonts/font1-regular.fntdata" {
			parts[i].body = parts[i].body[:len(parts[i].body)/2]
		}
	}
	findings := verifyBytes(t, rezip(t, parts))
	if rules(findings)[pptx.RuleFontContainer] == 0 {
		t.Errorf("a truncated font part was not reported, got %v", rules(findings))
	}
}
