// Verification of a written .pptx against what a strict OPC reader requires.
//
// This exists because four separate defects shipped from this package, each
// one accepted by every tool the repo had: python-pptx, LibreOffice, `unzip
// -t`, archive/zip's own reader, and a current PowerPoint. Each was found only
// after a file had been handed to someone whose PowerPoint was stricter, and
// three of the four were in the ZIP container rather than the XML, where none
// of the project's XML-aware checks would ever have looked.
//
// So the rules below are not a schema validator -- Sonar, html-validate and
// `go vet` analogues do not exist for OOXML, and writing one is not the job.
// They are the specific, cheap, byte-level invariants that the defects
// violated, plus the package-graph invariants that an audit had to be done by
// hand to catch. Every rule here corresponds to something that actually went
// wrong, or to something a hand audit had to check and would stop checking the
// moment nobody remembered to.
//
// `Write` runs this on its own output before renaming the temp file into
// place, so an invalid package cannot reach the destination at all. That is
// deliberate: an opt-in check is one nobody runs, and the whole lesson of the
// four defects is that the gap was never in the writing but in the checking.
//
// SCOPE, and it matters: these are invariants of *this writer's* output, not a
// general OOXML conformance suite. Some rules here are stricter than "will it
// open" -- a third-party .pptx in the wild was measured with data descriptors
// on all 248 entries and [Content_Types].xml not first, and LibreOffice opened
// it without complaint. Both of those remain findings here, because this
// writer deliberately does neither and a regression to them would be a real
// change in what it emits, in the exact area where it has already shipped
// three container defects. Do not point Verify at someone else's file and read
// its findings as defects in that file.
package pptx

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"image/png"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Rule names. Stable strings: they appear in build output and in tests.
const (
	RuleContentTypesFirst  = "content-types-first"
	RuleZipVersion         = "zip-version"
	RuleDataDescriptor     = "data-descriptor"
	RuleZipSizes           = "zip-sizes"
	RuleZipModDate         = "zip-mod-date"
	RuleEntryName          = "entry-name"
	RuleEntryCount         = "entry-count"
	RuleEncrypted          = "encrypted"
	RuleContentType        = "content-type"
	RuleXMLWellFormed      = "xml-well-formed"
	RuleRelationshipTarget = "relationship-target"
	RuleRelationshipID     = "relationship-id"
	RuleSlideIDList        = "slide-id-list"
	RuleThemePerMaster     = "theme-per-master"
	RuleSlideSize          = "slide-size"
	RuleMediaIntegrity     = "media-integrity"
	RuleFontEmbedding      = "font-embedding"
)

// Namespaces. Declared once; the attribute scan below compares against them.
const (
	nsRelationships = "http://schemas.openxmlformats.org/package/2006/relationships"
	nsOfficeRels    = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	nsContentTypes  = "http://schemas.openxmlformats.org/package/2006/content-types"
	nsPresentation  = "http://schemas.openxmlformats.org/presentationml/2006/main"

	relTypeTheme = nsOfficeRels + "/theme"
)

// Zip version floors from APPNOTE.TXT: 1.0 to store, 2.0 to Deflate. Zero is
// not a version at all -- it is what archive/zip's CreateRaw leaves behind,
// since only CreateHeader derives these fields.
const (
	zipVersionStore   = 10
	zipVersionDeflate = 20

	methodStore   = 0
	methodDeflate = 8

	flagEncrypted      = 0x0001
	flagDataDescriptor = 0x0008
)

// The legal range for a slide id, per the PresentationML schema.
const (
	minSlideID = 256
	maxSlideID = 2147483647
)

var (
	reSlidePart  = regexp.MustCompile(`^ppt/slides/slide\d+\.xml$`)
	reMasterPart = regexp.MustCompile(`^ppt/(slideMasters/slideMaster|notesMasters/notesMaster|handoutMasters/handoutMaster)\d+\.xml$`)
	reMediaPNG   = regexp.MustCompile(`^ppt/media/.+\.png$`)
)

// Finding is one way a package departs from what a strict reader requires.
type Finding struct {
	Rule   string
	Part   string // the entry or part concerned; empty for package-level
	Detail string
}

func (f Finding) String() string {
	if f.Part == "" {
		return fmt.Sprintf("%s: %s", f.Rule, f.Detail)
	}
	return fmt.Sprintf("%s: %s: %s", f.Rule, f.Part, f.Detail)
}

// FindingsError renders a set of findings as one error.
type FindingsError struct {
	Path     string
	Findings []Finding
}

func (e *FindingsError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "pptx: %s is not a conformant package (%d finding(s)):", e.Path, len(e.Findings))
	for _, f := range e.Findings {
		fmt.Fprintf(&b, "\n  %s", f)
	}
	return b.String()
}

// Verify reads the package at path and reports every departure it finds.
//
// It returns an error only when the file cannot be read or parsed as a ZIP at
// all; a malformed *package* comes back as findings, so a caller can report
// every problem in one pass rather than the first.
func Verify(path string) ([]Finding, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the package this writer just produced, or a path the operator named
	if err != nil {
		return nil, fmt.Errorf("pptx: verify %s: %w", path, err)
	}
	return VerifyBytes(raw)
}

// VerifyBytes is Verify over an in-memory package.
func VerifyBytes(raw []byte) ([]Finding, error) {
	entries, err := parseCentralDirectory(raw)
	if err != nil {
		return nil, err
	}

	var findings []Finding
	findings = append(findings, checkContainer(raw, entries)...)

	parts, bodies, readErrs := inflate(raw, entries)
	findings = append(findings, readErrs...)
	findings = append(findings, checkContentTypes(parts, bodies)...)

	rels, relFindings := parseAllRels(parts, bodies)
	findings = append(findings, relFindings...)
	findings = append(findings, checkXMLAndRelIDs(parts, bodies, rels)...)
	findings = append(findings, checkPresentation(parts, bodies, rels)...)
	findings = append(findings, checkThemePerMaster(parts, rels)...)
	findings = append(findings, checkMedia(parts, bodies)...)
	findings = append(findings, checkFonts(parts, bodies, rels)...)

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Rule != findings[j].Rule {
			return findings[i].Rule < findings[j].Rule
		}
		return findings[i].Part < findings[j].Part
	})
	return findings, nil
}

// ── container: the ZIP bytes themselves ─────────────────────────────────────

// entry is one central-directory record, paired with the local header it
// points at. Both are read: three of the four shipped defects were a field
// that disagreed between the two, or was zero in one of them.
type entry struct {
	name          string
	madeBy        uint16
	needed        uint16
	flags         uint16
	method        uint16
	crc           uint32
	compressed    uint64
	uncompressed  uint64
	dosDate       uint16
	dosTime       uint16
	localOffset   uint32
	localNeeded   uint16
	localFlags    uint16
	localMethod   uint16
	localCRC      uint32
	localComp     uint32
	localUncomp   uint32
	localHeaderOK bool
}

func parseCentralDirectory(raw []byte) ([]entry, error) {
	const eocdLen = 22
	i := bytes.LastIndex(raw, []byte("PK\x05\x06"))
	if i < 0 || i+eocdLen > len(raw) {
		return nil, fmt.Errorf("pptx: no end-of-central-directory record")
	}
	count := int(binary.LittleEndian.Uint16(raw[i+10:]))
	offset := int(binary.LittleEndian.Uint32(raw[i+16:]))

	entries := make([]entry, 0, count)
	for n := 0; n < count; n++ {
		if offset+46 > len(raw) || !bytes.Equal(raw[offset:offset+4], []byte("PK\x01\x02")) {
			return nil, fmt.Errorf("pptx: central directory record %d is not where the previous one ends", n)
		}
		nameLen := int(binary.LittleEndian.Uint16(raw[offset+28:]))
		extraLen := int(binary.LittleEndian.Uint16(raw[offset+30:]))
		commentLen := int(binary.LittleEndian.Uint16(raw[offset+32:]))
		if offset+46+nameLen > len(raw) {
			return nil, fmt.Errorf("pptx: central directory record %d has a truncated name", n)
		}
		e := entry{
			name:         string(raw[offset+46 : offset+46+nameLen]),
			madeBy:       binary.LittleEndian.Uint16(raw[offset+4:]),
			needed:       binary.LittleEndian.Uint16(raw[offset+6:]),
			flags:        binary.LittleEndian.Uint16(raw[offset+8:]),
			method:       binary.LittleEndian.Uint16(raw[offset+10:]),
			dosTime:      binary.LittleEndian.Uint16(raw[offset+12:]),
			dosDate:      binary.LittleEndian.Uint16(raw[offset+14:]),
			crc:          binary.LittleEndian.Uint32(raw[offset+16:]),
			compressed:   uint64(binary.LittleEndian.Uint32(raw[offset+20:])),
			uncompressed: uint64(binary.LittleEndian.Uint32(raw[offset+24:])),
			localOffset:  binary.LittleEndian.Uint32(raw[offset+42:]),
		}

		if lo := int(e.localOffset); lo+30 <= len(raw) && bytes.Equal(raw[lo:lo+4], []byte("PK\x03\x04")) {
			e.localHeaderOK = true
			e.localNeeded = binary.LittleEndian.Uint16(raw[lo+4:])
			e.localFlags = binary.LittleEndian.Uint16(raw[lo+6:])
			e.localMethod = binary.LittleEndian.Uint16(raw[lo+8:])
			e.localCRC = binary.LittleEndian.Uint32(raw[lo+14:])
			e.localComp = binary.LittleEndian.Uint32(raw[lo+18:])
			e.localUncomp = binary.LittleEndian.Uint32(raw[lo+22:])
		}
		entries = append(entries, e)
		offset += 46 + nameLen + extraLen + commentLen
	}
	return entries, nil
}

// minimumVersion is the lowest "version needed to extract" that honestly
// describes an entry compressed with method.
func minimumVersion(method uint16) uint16 {
	if method == methodDeflate {
		return zipVersionDeflate
	}
	return zipVersionStore
}

func checkContainer(raw []byte, entries []entry) []Finding {
	var out []Finding
	add := func(rule, part, format string, args ...any) {
		out = append(out, Finding{Rule: rule, Part: part, Detail: fmt.Sprintf(format, args...)})
	}

	if len(entries) == 0 {
		add(RuleEntryCount, "", "the package has no entries")
		return out
	}
	if entries[0].name != contentTypesPart {
		add(RuleContentTypesFirst, entries[0].name,
			"first entry must be %s, so a consumer reading the package as a stream finds the type map "+
				"before any part it has to type", contentTypesPart)
	}

	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e.name] {
			add(RuleEntryName, e.name, "duplicate entry name")
		}
		seen[e.name] = true

		switch {
		case strings.Contains(e.name, `\`):
			add(RuleEntryName, e.name, "name contains a backslash; OPC part names use forward slashes")
		case strings.HasPrefix(e.name, "/"):
			add(RuleEntryName, e.name, "name is absolute")
		default:
			for _, seg := range strings.Split(e.name, "/") {
				if seg == ".." {
					add(RuleEntryName, e.name, "name escapes the package root")
					break
				}
			}
		}

		if e.flags&flagEncrypted != 0 {
			add(RuleEncrypted, e.name, "the encryption bit is set")
		}
		if e.flags&flagDataDescriptor != 0 || e.localFlags&flagDataDescriptor != 0 {
			add(RuleDataDescriptor, e.name,
				"general-purpose bit 3 is set, so CRC and sizes trail the data instead of sitting in the local header; "+
					"this writer compresses up front and populates both, and the first defect it ever shipped was not doing so")
		}

		if !e.localHeaderOK {
			add(RuleZipSizes, e.name, "no local file header at the offset the central directory gives (%d)", e.localOffset)
			continue
		}

		floor := minimumVersion(e.method)
		if e.localNeeded < floor {
			add(RuleZipVersion, e.name,
				"local header declares version %d; method %d needs at least %d (0 is archive/zip's zero value, not a version)",
				e.localNeeded, e.method, floor)
		}
		if e.needed < floor {
			add(RuleZipVersion, e.name,
				"central directory declares version-needed %d; method %d needs at least %d", e.needed, e.method, floor)
		}
		if e.madeBy&0x00ff == 0 {
			add(RuleZipVersion, e.name, "central directory declares version-made-by 0, which is not a version")
		}

		if e.localCRC != e.crc || uint64(e.localComp) != e.compressed || uint64(e.localUncomp) != e.uncompressed {
			add(RuleZipSizes, e.name,
				"local header and central directory disagree: crc %08x/%08x, compressed %d/%d, uncompressed %d/%d",
				e.localCRC, e.crc, e.localComp, e.compressed, e.localUncomp, e.uncompressed)
		}
		if e.uncompressed > 0 && (e.localCRC == 0 || e.localComp == 0 || e.localUncomp == 0) {
			add(RuleZipSizes, e.name, "local header leaves CRC or sizes zero for a non-empty entry")
		}

		month := (e.dosDate >> 5) & 0x0f
		day := e.dosDate & 0x1f
		if month < 1 || month > 12 || day < 1 || day > 31 {
			add(RuleZipModDate, e.name,
				"MS-DOS date %#04x is month %d day %d, which is not a real date (the zero value writes month 0, day 0)",
				e.dosDate, month, day)
		}
	}
	return out
}

// ── package: content types, relationships, the presentation graph ──────────

const contentTypesPart = "[Content_Types].xml"

// inflate decompresses every entry. Part order is preserved so findings read
// in package order.
func inflate(raw []byte, entries []entry) ([]string, map[string][]byte, []Finding) {
	var findings []Finding
	names := make([]string, 0, len(entries))
	bodies := make(map[string][]byte, len(entries))

	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, nil, []Finding{{Rule: RuleZipSizes, Detail: fmt.Sprintf("the package will not open as a ZIP: %v", err)}}
	}
	for _, f := range zr.File {
		names = append(names, f.Name)
		rc, err := f.Open()
		if err != nil {
			findings = append(findings, Finding{Rule: RuleZipSizes, Part: f.Name,
				Detail: fmt.Sprintf("will not decompress: %v", err)})
			continue
		}
		var buf bytes.Buffer
		_, err = buf.ReadFrom(rc)
		_ = rc.Close()
		if err != nil {
			findings = append(findings, Finding{Rule: RuleZipSizes, Part: f.Name,
				Detail: fmt.Sprintf("decompressed short: %v", err)})
			continue
		}
		bodies[f.Name] = buf.Bytes()
	}
	return names, bodies, findings
}

type ctTypes struct {
	Defaults []struct {
		Extension   string `xml:"Extension,attr"`
		ContentType string `xml:"ContentType,attr"`
	} `xml:"Default"`
	Overrides []struct {
		PartName    string `xml:"PartName,attr"`
		ContentType string `xml:"ContentType,attr"`
	} `xml:"Override"`
}

// checkContentTypes is the rule a hand audit kept having to re-run: every part
// must be typed, by extension Default or by an explicit Override, and no
// Override may name a part that is not there.
func checkContentTypes(names []string, bodies map[string][]byte) []Finding {
	var out []Finding
	body, ok := bodies[contentTypesPart]
	if !ok {
		return []Finding{{Rule: RuleContentType, Part: contentTypesPart,
			Detail: "the package has no content-type map, so no part can be interpreted"}}
	}
	var ct ctTypes
	if err := xml.Unmarshal(body, &ct); err != nil {
		return []Finding{{Rule: RuleContentType, Part: contentTypesPart,
			Detail: fmt.Sprintf("will not parse: %v", err)}}
	}

	defaults := map[string]bool{}
	for _, d := range ct.Defaults {
		defaults[strings.ToLower(d.Extension)] = true
	}
	overrides := map[string]string{}
	for _, o := range ct.Overrides {
		overrides[o.PartName] = o.ContentType
	}

	present := map[string]bool{}
	for _, n := range names {
		present[n] = true
	}
	for _, n := range names {
		if n == contentTypesPart {
			continue
		}
		declared, viaOverride := overrides["/"+n]
		ext := ""
		if i := strings.LastIndex(n, "."); i >= 0 {
			ext = strings.ToLower(n[i+1:])
		}
		if !viaOverride && !defaults[ext] {
			out = append(out, Finding{Rule: RuleContentType, Part: n,
				Detail: fmt.Sprintf("no content type: extension %q has no Default and the part has no Override", ext)})
			continue
		}

		// "Typed" is not enough. Every .xml part matches the xml Default, so a
		// PresentationML part whose Override is missing silently becomes
		// application/xml -- still typed, and wrong. A reader that dispatches
		// on content type then does not know it is looking at a slide. So the
		// parts whose type is dictated are checked against the type they must
		// carry, not merely against having one.
		want, dictated := requiredContentType(n)
		if !dictated {
			continue
		}
		if !viaOverride {
			out = append(out, Finding{Rule: RuleContentType, Part: n,
				Detail: fmt.Sprintf("needs an explicit Override of %s; it is falling back to the %q Default", want, ext)})
			continue
		}
		if declared != want {
			out = append(out, Finding{Rule: RuleContentType, Part: n,
				Detail: fmt.Sprintf("declared content type is %s, must be %s", declared, want)})
		}
	}
	for part := range overrides {
		if !present[strings.TrimPrefix(part, "/")] {
			out = append(out, Finding{Rule: RuleContentType, Part: part,
				Detail: "an Override names this part, but the package does not contain it"})
		}
	}
	return out
}

// The content type each PresentationML part must declare. A part not listed
// here (media, .rels, a theme's own sub-parts) is governed by a Default.
var contentTypeByPattern = []struct {
	pattern *regexp.Regexp
	want    string
}{
	{regexp.MustCompile(`^ppt/presentation\.xml$`), "application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"},
	{regexp.MustCompile(`^ppt/slides/slide\d+\.xml$`), "application/vnd.openxmlformats-officedocument.presentationml.slide+xml"},
	{regexp.MustCompile(`^ppt/notesSlides/notesSlide\d+\.xml$`), "application/vnd.openxmlformats-officedocument.presentationml.notesSlide+xml"},
	{regexp.MustCompile(`^ppt/slideMasters/slideMaster\d+\.xml$`), "application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"},
	{regexp.MustCompile(`^ppt/notesMasters/notesMaster\d+\.xml$`), "application/vnd.openxmlformats-officedocument.presentationml.notesMaster+xml"},
	{regexp.MustCompile(`^ppt/slideLayouts/slideLayout\d+\.xml$`), "application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"},
	{regexp.MustCompile(`^ppt/theme/theme\d+\.xml$`), "application/vnd.openxmlformats-officedocument.theme+xml"},
	{regexp.MustCompile(`^ppt/presProps\.xml$`), "application/vnd.openxmlformats-officedocument.presentationml.presProps+xml"},
	{regexp.MustCompile(`^ppt/viewProps\.xml$`), "application/vnd.openxmlformats-officedocument.presentationml.viewProps+xml"},
	{regexp.MustCompile(`^ppt/tableStyles\.xml$`), "application/vnd.openxmlformats-officedocument.presentationml.tableStyles+xml"},
	{regexp.MustCompile(`^docProps/core\.xml$`), "application/vnd.openxmlformats-package.core-properties+xml"},
	{regexp.MustCompile(`^docProps/app\.xml$`), "application/vnd.openxmlformats-officedocument.extended-properties+xml"},
}

// requiredContentType reports the type part must declare, if one is dictated.
func requiredContentType(part string) (string, bool) {
	for _, row := range contentTypeByPattern {
		if row.pattern.MatchString(part) {
			return row.want, true
		}
	}
	return "", false
}

type relationship struct {
	ID         string `xml:"Id,attr"`
	Type       string `xml:"Type,attr"`
	Target     string `xml:"Target,attr"`
	TargetMode string `xml:"TargetMode,attr"`
}

type relsFile struct {
	Relationships []relationship `xml:"Relationship"`
}

// relsPath is the .rels part holding the relationships owned by part.
func relsPath(part string) string {
	return path.Join(path.Dir(part), "_rels", path.Base(part)+".rels")
}

// parseAllRels returns, per owning part, its relationship table, and reports
// every internal target that does not resolve to a part in the package.
func parseAllRels(names []string, bodies map[string][]byte) (map[string]map[string]relationship, []Finding) {
	var out []Finding
	present := map[string]bool{}
	for _, n := range names {
		present[n] = true
	}

	tables := map[string]map[string]relationship{}
	for _, n := range names {
		if !strings.HasSuffix(n, ".rels") {
			continue
		}
		var rf relsFile
		if err := xml.Unmarshal(bodies[n], &rf); err != nil {
			out = append(out, Finding{Rule: RuleXMLWellFormed, Part: n,
				Detail: fmt.Sprintf("will not parse: %v", err)})
			continue
		}
		// _rels/x.rels describes the part x, which sits one directory up.
		base := path.Dir(path.Dir(n))
		owner := path.Join(base, strings.TrimSuffix(path.Base(n), ".rels"))
		if path.Base(n) == ".rels" {
			owner = "" // the package relationships
		}
		table := map[string]relationship{}
		for _, rel := range rf.Relationships {
			table[rel.ID] = rel
			if rel.TargetMode == "External" {
				continue
			}
			resolved := path.Join(base, rel.Target)
			if !present[resolved] {
				out = append(out, Finding{Rule: RuleRelationshipTarget, Part: n,
					Detail: fmt.Sprintf("%s targets %q, which resolves to %s and is not in the package",
						rel.ID, rel.Target, resolved)})
			}
		}
		tables[owner] = table
	}
	return tables, out
}

// checkXMLAndRelIDs parses every XML part and asserts that each r:id it uses
// has a matching relationship in that part's own .rels.
//
// A dangling r:id is the shape that renders a blank region rather than
// failing, which is why it needs a check rather than a reader's opinion.
func checkXMLAndRelIDs(names []string, bodies map[string][]byte, rels map[string]map[string]relationship) []Finding {
	var out []Finding
	for _, n := range names {
		if !strings.HasSuffix(n, ".xml") || strings.HasSuffix(n, ".rels") {
			continue
		}
		used := map[string]bool{}
		dec := xml.NewDecoder(bytes.NewReader(bodies[n]))
		for {
			tok, err := dec.Token()
			if err != nil {
				if err.Error() != "EOF" {
					out = append(out, Finding{Rule: RuleXMLWellFormed, Part: n,
						Detail: fmt.Sprintf("is not well-formed: %v", err)})
				}
				break
			}
			se, ok := tok.(xml.StartElement)
			if !ok {
				continue
			}
			for _, attr := range se.Attr {
				if attr.Name.Space == nsOfficeRels {
					used[attr.Value] = true
				}
			}
		}

		table := rels[n]
		missing := make([]string, 0, len(used))
		for id := range used {
			if _, ok := table[id]; !ok {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			out = append(out, Finding{Rule: RuleRelationshipID, Part: n,
				Detail: fmt.Sprintf("uses %v with no matching relationship in %s", missing, relsPath(n))})
		}
	}
	return out
}

// ── presentation graph ─────────────────────────────────────────────────────

// sldRef is one sldIdLst entry: its own id, and the relationship id naming
// the slide part.
type sldRef struct {
	id  string
	rid string
}

// parsePresentation reads sldIdLst and sldSz by scanning tokens rather than
// unmarshalling.
//
// `xml:"id,attr"` would have matched BOTH `id` and `r:id` -- a tag with no
// namespace matches any namespace in encoding/xml -- so a struct bound the
// relationship id into the slide id and every deck looked like it had an
// out-of-range sldId. Comparing attr.Name.Space is the only way to tell the
// two apart.
func parsePresentation(body []byte) ([]sldRef, string, string, error) {
	var refs []sldRef
	var cx, cy string

	dec := xml.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, "", "", err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "sldId":
			var ref sldRef
			for _, a := range se.Attr {
				switch {
				case a.Name.Local == "id" && a.Name.Space == "":
					ref.id = a.Value
				case a.Name.Local == "id" && a.Name.Space == nsOfficeRels:
					ref.rid = a.Value
				}
			}
			refs = append(refs, ref)
		case "sldSz":
			for _, a := range se.Attr {
				switch a.Name.Local {
				case "cx":
					cx = a.Value
				case "cy":
					cy = a.Value
				}
			}
		}
	}
	return refs, cx, cy, nil
}

// checkPresentation asserts the slide list and the slide parts agree.
//
// A dropped or doubled slide is invisible until someone presents, which is the
// same reason `pdf-pages` exists for the PDF path.
func checkPresentation(names []string, bodies map[string][]byte, rels map[string]map[string]relationship) []Finding {
	var out []Finding
	const presPart = "ppt/presentation.xml"
	body, ok := bodies[presPart]
	if !ok {
		return []Finding{{Rule: RuleSlideIDList, Part: presPart, Detail: "the package has no presentation part"}}
	}
	refs, cxAttr, cyAttr, err := parsePresentation(body)
	if err != nil {
		return []Finding{{Rule: RuleXMLWellFormed, Part: presPart, Detail: fmt.Sprintf("will not parse: %v", err)}}
	}

	cx, errX := strconv.Atoi(cxAttr)
	cy, errY := strconv.Atoi(cyAttr)
	if errX != nil || errY != nil || cx <= 0 || cy <= 0 {
		out = append(out, Finding{Rule: RuleSlideSize, Part: presPart,
			Detail: fmt.Sprintf("sldSz is cx=%q cy=%q; both must be positive EMU", cxAttr, cyAttr)})
	}

	table := rels[presPart]
	listed := map[string]bool{}
	seenID := map[string]bool{}
	for _, ref := range refs {
		if seenID[ref.id] {
			out = append(out, Finding{Rule: RuleSlideIDList, Part: presPart,
				Detail: fmt.Sprintf("duplicate sldId id=%s", ref.id)})
		}
		seenID[ref.id] = true

		if n, err := strconv.Atoi(ref.id); err != nil || n < minSlideID || n > maxSlideID {
			out = append(out, Finding{Rule: RuleSlideIDList, Part: presPart,
				Detail: fmt.Sprintf("sldId id=%q is outside the legal range %d..%d", ref.id, minSlideID, maxSlideID)})
		}

		rel, ok := table[ref.rid]
		if !ok {
			out = append(out, Finding{Rule: RuleSlideIDList, Part: presPart,
				Detail: fmt.Sprintf("sldId id=%s uses %q, which has no relationship", ref.id, ref.rid)})
			continue
		}
		listed[path.Join("ppt", rel.Target)] = true
	}

	var actual []string
	for _, n := range names {
		if reSlidePart.MatchString(n) {
			actual = append(actual, n)
		}
	}
	for _, n := range actual {
		if !listed[n] {
			out = append(out, Finding{Rule: RuleSlideIDList, Part: n,
				Detail: "the package contains this slide but sldIdLst does not list it, so it will not be shown"})
		}
	}
	for n := range listed {
		if !reSlidePart.MatchString(n) {
			continue
		}
		if _, ok := bodies[n]; !ok {
			out = append(out, Finding{Rule: RuleSlideIDList, Part: n, Detail: "sldIdLst lists this slide, which is missing"})
		}
	}
	return out
}

// checkThemePerMaster is the fourth shipped defect.
//
// ECMA-376 allocates a theme "per Handout Master, Notes Master, Slide Master
// or Presentation part". Pointing two MASTERS at one theme part opens happily
// in python-pptx and LibreOffice, and makes a desktop PowerPoint refuse the
// whole document with a generic repair prompt.
//
// Only masters are compared. The presentation part legitimately shares the
// slide master's theme -- PowerPoint's own output does -- so a broader "one
// theme per part" rule would be false and would reject conformant files.
func checkThemePerMaster(names []string, rels map[string]map[string]relationship) []Finding {
	users := map[string][]string{}
	var masters []string
	for _, n := range names {
		if !reMasterPart.MatchString(n) {
			continue
		}
		masters = append(masters, n)
		for _, rel := range rels[n] {
			if rel.Type != relTypeTheme {
				continue
			}
			theme := path.Join(path.Dir(n), rel.Target)
			users[theme] = append(users[theme], n)
		}
	}

	var out []Finding
	for _, m := range masters {
		hasTheme := false
		for _, rel := range rels[m] {
			if rel.Type == relTypeTheme {
				hasTheme = true
				break
			}
		}
		if !hasTheme {
			out = append(out, Finding{Rule: RuleThemePerMaster, Part: m, Detail: "declares no theme relationship"})
		}
	}
	for theme, who := range users {
		if len(who) < 2 {
			continue
		}
		sort.Strings(who)
		out = append(out, Finding{Rule: RuleThemePerMaster, Part: theme,
			Detail: fmt.Sprintf("shared by %d masters (%s); each master needs its own theme part",
				len(who), strings.Join(who, ", "))})
	}
	return out
}

// checkMedia fully decodes every PNG.
//
// Decoding, not sniffing: a slide image PowerPoint began writing and abandoned
// has a valid signature and valid chunk CRCs, and only decompressing the pixel
// data shows the stream is short.
func checkMedia(names []string, bodies map[string][]byte) []Finding {
	var out []Finding
	for _, n := range names {
		if !reMediaPNG.MatchString(n) {
			continue
		}
		body := bodies[n]
		if len(body) == 0 {
			out = append(out, Finding{Rule: RuleMediaIntegrity, Part: n, Detail: "is empty"})
			continue
		}
		if _, err := png.Decode(bytes.NewReader(body)); err != nil {
			out = append(out, Finding{Rule: RuleMediaIntegrity, Part: n,
				Detail: fmt.Sprintf("is not a complete PNG: %v", err)})
		}
	}
	return out
}

// ── fonts ───────────────────────────────────────────────────────────────────

var reFontPart = regexp.MustCompile(`^ppt/fonts/.+\.fntdata$`)

// reEmbeddedFont matches one <p:embeddedFont> block, and reLatin a run's
// declared typeface. Regex rather than a parser: these are two shapes in a
// document this package wrote itself, and unmarshalling presentation.xml into
// a struct would need the whole schema to reach two of its elements.
var (
	reEmbeddedFont = regexp.MustCompile(`(?s)<p:embeddedFont>(.*?)</p:embeddedFont>`)
	reFontTypeface = regexp.MustCompile(`<p:font typeface="([^"]*)"`)
	reFontVariant  = regexp.MustCompile(`<p:(regular|bold|italic|boldItalic) r:id="([^"]+)"`)
	reLatin        = regexp.MustCompile(`<a:latin typeface="([^"]*)"`)
)

// checkFonts holds the structure of an embedded typeface.
//
// Whether a typeface will SUBSTITUTE is not checked here: a package naming a
// font the reader lacks is not malformed, it is a trade-off, and Fonts()
// reports it so a build can say so. What is malformed is a font declaration
// that does not resolve -- an <p:embeddedFont> whose data is missing reaches
// PowerPoint as a font it was promised and cannot find.
func checkFonts(names []string, bodies map[string][]byte, rels map[string]map[string]relationship) []Finding {
	var out []Finding
	const presPart = "ppt/presentation.xml"
	body, ok := bodies[presPart]
	if !ok {
		return nil
	}
	pres := string(body)
	table := rels[presPart]
	present := map[string]bool{}
	for _, n := range names {
		present[n] = true
	}

	for _, block := range reEmbeddedFont.FindAllStringSubmatch(pres, -1) {
		typeface := ""
		if m := reFontTypeface.FindStringSubmatch(block[1]); m != nil {
			typeface = m[1]
		}
		if strings.TrimSpace(typeface) == "" {
			out = append(out, Finding{Rule: RuleFontEmbedding, Part: presPart,
				Detail: "an embedded font declares no typeface, so nothing can reference it"})
		}

		variants := reFontVariant.FindAllStringSubmatch(block[1], -1)
		if len(variants) == 0 {
			out = append(out, Finding{Rule: RuleFontEmbedding, Part: presPart,
				Detail: fmt.Sprintf("embedded font %q declares no font data", typeface)})
		}
		for _, v := range variants {
			rel, found := table[v[2]]
			if !found {
				out = append(out, Finding{Rule: RuleFontEmbedding, Part: presPart,
					Detail: fmt.Sprintf("embedded font %q names %s for its %s, which has no relationship",
						typeface, v[2], v[1])})
				continue
			}
			target := path.Join("ppt", rel.Target)
			if !present[target] {
				out = append(out, Finding{Rule: RuleFontEmbedding, Part: presPart,
					Detail: fmt.Sprintf("embedded font %q points at %s, which the package does not contain",
						typeface, target)})
				continue
			}
			if len(bodies[target]) == 0 {
				out = append(out, Finding{Rule: RuleFontEmbedding, Part: target,
					Detail: fmt.Sprintf("embedded font %q has an empty data part", typeface)})
			}
			if rel.Type != fontRelType {
				out = append(out, Finding{Rule: RuleFontEmbedding, Part: presPart,
					Detail: fmt.Sprintf("embedded font %q uses %s, whose relationship type is %s, not a font",
						typeface, v[2], rel.Type)})
			}
		}
	}

	// A font part nothing declares is dead weight travelling in the package,
	// and usually means a declaration was dropped rather than the data.
	declared := map[string]bool{}
	for _, rel := range table {
		if rel.Type == fontRelType {
			declared[path.Join("ppt", rel.Target)] = true
		}
	}
	for _, n := range names {
		if reFontPart.MatchString(n) && !declared[n] {
			out = append(out, Finding{Rule: RuleFontEmbedding, Part: n,
				Detail: "font data that no embeddedFont declaration references"})
		}
	}

	// An empty typeface in a run is not "inherit", it is a run that names
	// nothing, and PowerPoint resolves it to its own default rather than the
	// theme's.
	for _, n := range names {
		if !strings.HasPrefix(n, "ppt/slides/slide") && n != "ppt/theme/theme1.xml" {
			continue
		}
		for _, m := range reLatin.FindAllStringSubmatch(string(bodies[n]), -1) {
			if strings.TrimSpace(m[1]) == "" {
				out = append(out, Finding{Rule: RuleFontEmbedding, Part: n,
					Detail: "a run or fontScheme declares an empty typeface"})
				break
			}
		}
	}
	return out
}

// Fonts reports, for every typeface the package names, whether a reader will
// actually see it.
//
// This is the honest answer to "will the fonts work": a typeface is safe when
// the package carries it or when it ships with Windows or Office, and
// otherwise it will substitute on some machine. Reported rather than enforced
// -- naming a font the reader lacks is a trade-off a deck is allowed to make,
// as long as it is made knowingly.
func Fonts(path string) ([]FontReport, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the package this writer produced, or a path the operator named
	if err != nil {
		return nil, fmt.Errorf("pptx: fonts %s: %w", path, err)
	}
	entries, err := parseCentralDirectory(raw)
	if err != nil {
		return nil, err
	}
	names, bodies, _ := inflate(raw, entries)

	embedded := map[string]bool{}
	for _, block := range reEmbeddedFont.FindAllStringSubmatch(string(bodies["ppt/presentation.xml"]), -1) {
		if m := reFontTypeface.FindStringSubmatch(block[1]); m != nil {
			embedded[strings.ToLower(m[1])] = true
		}
	}

	seen := map[string]bool{}
	var out []FontReport
	for _, n := range names {
		if !strings.HasSuffix(n, ".xml") || strings.HasSuffix(n, ".rels") {
			continue
		}
		for _, m := range reLatin.FindAllStringSubmatch(string(bodies[n]), -1) {
			face := strings.TrimSpace(m[1])
			if face == "" || seen[strings.ToLower(face)] {
				continue
			}
			// "+mj-lt" and "+mn-lt" are references INTO the theme's
			// fontScheme, not typeface names: the master and layouts use them
			// so a theme change repoints every run. Reporting them as fonts
			// nobody has was a false positive on every single build, which is
			// how a warning stops being read.
			if strings.HasPrefix(face, "+") {
				continue
			}
			seen[strings.ToLower(face)] = true
			out = append(out, FontReport{
				Typeface:       face,
				Embedded:       embedded[strings.ToLower(face)],
				AssumedPresent: AssumedPresent(face),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Typeface < out[j].Typeface })
	return out, nil
}
