package pptx

import (
	"encoding/binary"
	"fmt"
	"unicode/utf16"
)

// Minimal sfnt reader, for the three tables an EOT header is built from.
//
// Deliberately not golang.org/x/image/font/sfnt: that package parses a font
// for rendering and does not expose the raw OS/2 fields an EOT header needs
// (fsType, the unicode and code-page range bitmaps, PANOSE), which are the
// only reason to open the file at all. Three tables, read by offset, is less
// code than adapting a renderer's model would be -- and one fewer dependency
// in a package whose whole job is byte layout.
type sfntInfo struct {
	Weight    uint32
	Italic    bool
	FsType    uint16
	Panose    [10]byte
	Unicode   [4]uint32
	CodePage  [2]uint32
	ChecksumA uint32

	Family  string
	Style   string
	Version string
	Full    string
}

// sfnt table tags, and the two container magics that introduce a table
// directory. A .ttc font collection is deliberately not accepted: it holds
// several fonts and nothing here could say which one was meant.
const (
	tagOS2  = "OS/2"
	tagHead = "head"
	tagName = "name"

	sfntVersionTrueType = 0x00010000
	sfntVersionCFF      = 0x4F54544F // "OTTO"
)

// sfntTables indexes a font's table directory by tag.
func sfntTables(raw []byte) (map[string][]byte, error) {
	if len(raw) < 12 {
		return nil, fmt.Errorf("not a font: %d bytes is shorter than an sfnt header", len(raw))
	}
	switch v := binary.BigEndian.Uint32(raw); v {
	case sfntVersionTrueType, sfntVersionCFF:
	default:
		return nil, fmt.Errorf("not a TrueType or OpenType font: leading bytes are %#08x", v)
	}

	n := int(binary.BigEndian.Uint16(raw[4:]))
	if want := 12 + n*16; len(raw) < want {
		return nil, fmt.Errorf("font declares %d tables, which needs %d bytes and the file has %d", n, want, len(raw))
	}

	out := make(map[string][]byte, n)
	for i := 0; i < n; i++ {
		rec := raw[12+i*16:]
		tag := string(rec[0:4])
		off := int(binary.BigEndian.Uint32(rec[8:]))
		length := int(binary.BigEndian.Uint32(rec[12:]))
		if off < 0 || length < 0 || off+length > len(raw) {
			return nil, fmt.Errorf("table %q runs from %d for %d bytes, past the end of a %d-byte file",
				tag, off, length, len(raw))
		}
		out[tag] = raw[off : off+length]
	}
	return out, nil
}

// readSfnt pulls the EOT header's inputs out of a font's own tables.
func readSfnt(raw []byte) (sfntInfo, error) {
	tables, err := sfntTables(raw)
	if err != nil {
		return sfntInfo{}, err
	}

	var info sfntInfo
	if err := readOS2(tables[tagOS2], &info); err != nil {
		return sfntInfo{}, err
	}
	if err := readHead(tables[tagHead], &info); err != nil {
		return sfntInfo{}, err
	}
	readNames(tables[tagName], &info)
	if info.Family == "" {
		return sfntInfo{}, fmt.Errorf("font declares no family name, so nothing could reference it once embedded")
	}
	return info, nil
}

// OS/2 field offsets, per the OpenType specification. Named rather than
// inlined because an off-by-two here is a wrong weight in the output and
// nothing would notice.
const (
	os2Version      = 0
	os2WeightClass  = 4
	os2FsType       = 8
	os2Panose       = 32
	os2UnicodeRange = 42
	os2FsSelection  = 62
	os2CodePage     = 78

	os2MinLen         = 78 // through usWinDescent: version 0 has no code pages
	os2CodePageMinLen = 86 // through ulCodePageRange2: version 1 and later

	fsSelectionItalic = 1 << 0
)

func readOS2(t []byte, info *sfntInfo) error {
	if len(t) < os2MinLen {
		return fmt.Errorf("font has no usable OS/2 table (%d bytes), so its weight and embedding "+
			"permission cannot be read", len(t))
	}
	info.Weight = uint32(binary.BigEndian.Uint16(t[os2WeightClass:]))
	info.FsType = binary.BigEndian.Uint16(t[os2FsType:])
	copy(info.Panose[:], t[os2Panose:os2Panose+10])
	for i := range info.Unicode {
		info.Unicode[i] = binary.BigEndian.Uint32(t[os2UnicodeRange+i*4:])
	}
	info.Italic = binary.BigEndian.Uint16(t[os2FsSelection:])&fsSelectionItalic != 0

	// Code-page ranges arrived in OS/2 version 1. A version-0 table is not an
	// error: the fields are simply left zero, which is what "no declared code
	// pages" means in an EOT header too.
	if binary.BigEndian.Uint16(t[os2Version:]) >= 1 && len(t) >= os2CodePageMinLen {
		for i := range info.CodePage {
			info.CodePage[i] = binary.BigEndian.Uint32(t[os2CodePage+i*4:])
		}
	}
	return nil
}

// head field offsets, per the OpenType specification.
const (
	headChecksumAdjustment = 8
	headMagic              = 12
	headMacStyle           = 44

	headMinLen   = 54
	headMagicNum = 0x5F0F3CF5

	macStyleItalic = 1 << 1
)

func readHead(t []byte, info *sfntInfo) error {
	if len(t) < headMinLen {
		return fmt.Errorf("font has no usable head table (%d bytes)", len(t))
	}
	if got := binary.BigEndian.Uint32(t[headMagic:]); got != headMagicNum {
		return fmt.Errorf("head table magic is %#08x, not %#08x, so this is not a well-formed font",
			got, headMagicNum)
	}
	info.ChecksumA = binary.BigEndian.Uint32(t[headChecksumAdjustment:])

	// Either table may carry the italic bit and fonts disagree in practice, so
	// take the union rather than trusting one.
	if binary.BigEndian.Uint16(t[headMacStyle:])&macStyleItalic != 0 {
		info.Italic = true
	}
	return nil
}

// name table IDs for the four strings an EOT header carries.
const (
	nameFamily  = 1
	nameStyle   = 2
	nameFull    = 4
	nameVersion = 5

	// Windows platform, UCS-2 encoding. Preferred over the Macintosh records
	// because an EOT header's strings are UTF-16 and these already are, so no
	// transcoding guesswork is involved.
	platformWindows    = 3
	encodingWindowsBMP = 1

	nameHeaderLen = 6
	nameRecordLen = 12
)

// readNames fills in the four name strings, preferring the Windows records.
//
// A missing string is left empty rather than defaulted: an EOT header states
// a zero length for it, which is accurate, where inventing one would not be.
func readNames(t []byte, info *sfntInfo) {
	if len(t) < nameHeaderLen {
		return
	}
	count := int(binary.BigEndian.Uint16(t[2:]))
	storage := int(binary.BigEndian.Uint16(t[4:]))

	into := map[uint16]*string{
		nameFamily: &info.Family, nameStyle: &info.Style,
		nameFull: &info.Full, nameVersion: &info.Version,
	}
	for i := 0; i < count; i++ {
		at := nameHeaderLen + i*nameRecordLen
		if at+nameRecordLen > len(t) {
			return
		}
		rec := t[at:]
		platform := binary.BigEndian.Uint16(rec[0:])
		encoding := binary.BigEndian.Uint16(rec[2:])
		id := binary.BigEndian.Uint16(rec[6:])
		field, wanted := into[id]
		if !wanted || platform != platformWindows || encoding != encodingWindowsBMP || *field != "" {
			continue
		}
		length := int(binary.BigEndian.Uint16(rec[8:]))
		off := storage + int(binary.BigEndian.Uint16(rec[10:]))
		if off < 0 || length < 0 || off+length > len(t) {
			continue
		}
		*field = decodeUTF16BE(t[off : off+length])
	}
}

// decodeUTF16BE reads a big-endian UTF-16 name-table string.
func decodeUTF16BE(raw []byte) string {
	units := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		units = append(units, binary.BigEndian.Uint16(raw[i:]))
	}
	return string(utf16.Decode(units))
}
