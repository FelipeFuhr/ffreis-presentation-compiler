package pptx

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"unicode/utf16"
)

// The EOT (Embedded OpenType) container PowerPoint expects in a .fntdata part.
//
// WHY A HEADER AT ALL, since this package used to write the raw .ttf bytes.
//
// A real .pptx written by PowerPoint, with two embedded faces, settles the
// format: each .fntdata begins with a 260-byte header whose MagicNumber is
// 0x504C, whose Version is 0x00020002, and whose FontDataSize is exactly the
// byte count that follows it. Headerless sfnt carries none of that -- no
// magic for a reader to recognise, and no family name, weight or italic flag
// for it to match against the <a:latin typeface="..."/> that is supposed to
// resolve to the embedded face. A reader has nothing to go on.
//
// WHAT IS STILL NOT CERTAIN, and it is the compression flag.
//
// PowerPoint's own files set TTEMBED_TTCOMPRESSED and carry an MTX-compressed
// payload; the file examined began 03 01 49 20, which is not an sfnt magic.
// MTX (MicroType Express) is a glyph-level codec and implementing it blind,
// against no reference decoder available here, would be guesswork of exactly
// the kind that shipped the last font defect. So this writes a valid
// UNCOMPRESSED EOT -- the flag clear, the payload plain sfnt -- which the
// format permits and which every field above describes correctly.
//
// That is strictly closer to what PowerPoint writes than no header, and it is
// verifiable: eotHeaderOf parses it back, Verify checks it, and the field
// layout was derived from a PowerPoint-authored file rather than from prose.
// Whether PowerPoint *insists* on compression cannot be established without
// PowerPoint, and acceptance/powerpoint/ is where that question belongs.
const (
	eotVersion0200002 = 0x00020002
	eotMagicNumber    = 0x504C

	// eotFlagCompressed marks an MTX-compressed payload. Deliberately NOT
	// set: see the note above.
	eotFlagCompressed = 0x00000004

	// eotCharsetDefault is DEFAULT_CHARSET. PowerPoint wrote 1 for a Latin
	// face, so 1 is what a reader has been seen to accept.
	eotCharsetDefault = 1
)

// fsType embedding permissions, per the OpenType OS/2 specification.
//
// fsType is a licence, not a hint. Bit 1 means the vendor forbids embedding
// outright, and bits 2 and 3 are the permissive cases. The field is a bitmask
// whose low four bits are mutually exclusive in practice, so the test is for
// the restricted bit specifically rather than for equality with zero --
// treating "preview & print" or "editable" as a refusal would reject most
// commercial fonts that explicitly allow what this does.
const (
	fsTypeRestricted = 0x0002

	// fsTypeBitmapOnly forbids embedding outline data, which is the only kind
	// of data there is to embed here.
	fsTypeBitmapOnly = 0x0200
)

// ErrEmbeddingRestricted is returned for a font whose own licence bits forbid
// embedding it. Not a warning: shipping the face anyway would put a licence
// violation in a client's file.
var ErrEmbeddingRestricted = errors.New("the font's fsType bits forbid embedding")

// eotWrap turns sfnt font bytes into a .fntdata payload.
func eotWrap(sfnt []byte) ([]byte, error) {
	info, err := readSfnt(sfnt)
	if err != nil {
		return nil, fmt.Errorf("pptx: %w", err)
	}
	switch {
	case info.FsType&fsTypeRestricted != 0:
		return nil, fmt.Errorf("pptx: %w (fsType %#04x is Restricted License Embedding): %s",
			ErrEmbeddingRestricted, info.FsType, info.Family)
	case info.FsType&fsTypeBitmapOnly != 0:
		return nil, fmt.Errorf("pptx: %w (fsType %#04x permits bitmap embedding only, and there "+
			"are no bitmaps to embed): %s", ErrEmbeddingRestricted, info.FsType, info.Family)
	}

	// Every size an EOT header states is fixed width, so a font or a name too
	// large to express has to be refused rather than silently truncated: a
	// wrapped length would declare a payload shorter than the one present and
	// a reader would stop part-way through the glyphs.
	names := []string{info.Family, info.Style, info.Version, info.Full}
	for i, n := range names {
		if encoded := len(utf16.Encode([]rune(n))) * 2; encoded > math.MaxUint16 {
			return nil, fmt.Errorf("pptx: font name %d is %d bytes, more than an EOT header can state", i, encoded)
		}
	}

	var body bytes.Buffer
	writeEOTFixed(&body, info)
	for _, s := range names {
		writeEOTString(&body, s)
	}
	writeEOTTrailer(&body)

	if total := body.Len() + len(sfnt); total > math.MaxUint32 {
		return nil, fmt.Errorf("pptx: font %s is %d bytes, more than an EOT header can state", info.Family, total)
	}

	// EOTSize counts the header and the payload; FontDataSize counts only
	// what follows the header. Both were confirmed against a
	// PowerPoint-authored file, where EOTSize-FontDataSize was exactly the
	// header length.
	out := make([]byte, 0, body.Len()+len(sfnt))
	out = append(out, body.Bytes()...)
	out = append(out, sfnt...)
	binary.LittleEndian.PutUint32(out[0:], uint32(len(out)))  //nolint:gosec // bounded above
	binary.LittleEndian.PutUint32(out[4:], uint32(len(sfnt))) //nolint:gosec // bounded above
	return out, nil
}

// writeEOTFixed writes the fixed-size part of the header. The two sizes are
// placeholders, patched once the total is known.
func writeEOTFixed(b *bytes.Buffer, info sfntInfo) {
	le := func(v uint32) { _ = binary.Write(b, binary.LittleEndian, v) }
	le16 := func(v uint16) { _ = binary.Write(b, binary.LittleEndian, v) }

	le(0) // EOTSize, patched
	le(0) // FontDataSize, patched
	le(eotVersion0200002)
	le(0) // Flags: uncompressed, unsubsetted
	b.Write(info.Panose[:])
	b.WriteByte(eotCharsetDefault)
	if info.Italic {
		b.WriteByte(1)
	} else {
		b.WriteByte(0)
	}
	le(info.Weight)
	le16(info.FsType)
	le16(eotMagicNumber)
	for _, r := range info.Unicode {
		le(r)
	}
	for _, r := range info.CodePage {
		le(r)
	}
	le(info.ChecksumA)
	for i := 0; i < 4; i++ {
		le(0) // Reserved1..4
	}
}

// writeEOTString writes one padded, length-prefixed UTF-16LE header string.
// Callers bound the length before getting here; see eotWrap.
func writeEOTString(b *bytes.Buffer, s string) {
	encoded := utf16.Encode([]rune(s))
	_ = binary.Write(b, binary.LittleEndian, uint16(0))              // Padding
	_ = binary.Write(b, binary.LittleEndian, uint16(len(encoded)*2)) //nolint:gosec // bounded in eotWrap
	for _, u := range encoded {
		_ = binary.Write(b, binary.LittleEndian, u)
	}
}

// writeEOTTrailer writes the version-0x00020002 tail: an empty RootString, no
// EUDC font and no signature. PowerPoint wrote exactly these zeros for a
// face with no end-user defined characters, which is every face here.
func writeEOTTrailer(b *bytes.Buffer) {
	le := func(v uint32) { _ = binary.Write(b, binary.LittleEndian, v) }
	writeEOTString(b, "") // RootString
	le(0)                 // RootStringCheckSum
	le(0)                 // EUDCCodePage
	writeEOTString(b, "") // Signature
	le(0)                 // EUDCFlags
	le(0)                 // EUDCFontSize
}

// eotHeader is what a .fntdata part declares about itself.
type eotHeader struct {
	EOTSize      uint32
	FontDataSize uint32
	Version      uint32
	Flags        uint32
	Weight       uint32
	FsType       uint16
	Magic        uint16
	Italic       bool
	Family       string
}

// Compressed reports whether the payload is MTX-compressed rather than plain
// sfnt, which decides whether the font data can be read back at all.
func (h eotHeader) Compressed() bool { return h.Flags&eotFlagCompressed != 0 }

// eotFixedLen is the offset of the first header string, which is also the
// shortest a well-formed header can be.
const eotFixedLen = 0x52

// eotHeaderOf parses a .fntdata part.
//
// Exists so the writer is checked by reading its own output back rather than
// by asserting against a hand-copied byte string, and so Verify can tell an
// EOT container from whatever else a part might contain.
func eotHeaderOf(raw []byte) (eotHeader, error) {
	if len(raw) < eotFixedLen+2 {
		return eotHeader{}, fmt.Errorf("%d bytes is shorter than an EOT header", len(raw))
	}
	h := eotHeader{
		EOTSize:      binary.LittleEndian.Uint32(raw[0:]),
		FontDataSize: binary.LittleEndian.Uint32(raw[4:]),
		Version:      binary.LittleEndian.Uint32(raw[8:]),
		Flags:        binary.LittleEndian.Uint32(raw[12:]),
		Italic:       raw[0x1B] != 0,
		Weight:       binary.LittleEndian.Uint32(raw[0x1C:]),
		FsType:       binary.LittleEndian.Uint16(raw[0x20:]),
		Magic:        binary.LittleEndian.Uint16(raw[0x22:]),
	}
	if h.Magic != eotMagicNumber {
		return h, fmt.Errorf("MagicNumber is %#04x, not the EOT %#04x", h.Magic, eotMagicNumber)
	}

	size := int(binary.LittleEndian.Uint16(raw[eotFixedLen:]))
	if at := eotFixedLen + 2; at+size <= len(raw) {
		units := make([]uint16, 0, size/2)
		for i := at; i+1 < at+size; i += 2 {
			units = append(units, binary.LittleEndian.Uint16(raw[i:]))
		}
		h.Family = string(utf16.Decode(units))
	}
	return h, nil
}
