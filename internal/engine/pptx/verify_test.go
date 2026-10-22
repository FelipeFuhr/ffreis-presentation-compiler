package pptx_test

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"regexp"
	"strconv"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine/pptx"
)

// ── fixtures ────────────────────────────────────────────────────────────────

// goodPackage is what the writer currently emits, as bytes.
func goodPackage(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(writeDeck(t,
		pptx.Slide{PNG: onePixelPNG, Notes: "first"},
		pptx.Slide{PNG: onePixelPNG, Notes: "second"},
	))
	if err != nil {
		t.Fatalf("read package: %v", err)
	}
	return raw
}

type namedPart struct {
	name string
	body []byte
}

// explode returns every part, in package order.
func explode(t *testing.T, raw []byte) []namedPart {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("open package: %v", err)
	}
	out := make([]namedPart, 0, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(rc); err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		_ = rc.Close()
		out = append(out, namedPart{f.Name, buf.Bytes()})
	}
	return out
}

// rezip rebuilds a package from parts the way the writer does.
//
// It must mirror add(): archive/zip's CreateHeader path appends a data
// descriptor and leaves the local header's CRC and sizes zero, so rebuilding
// through it would trip data-descriptor and zip-sizes on every fixture and
// make it impossible to test one rule at a time.
func rezip(t *testing.T, parts []namedPart) []byte {
	t.Helper()
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for _, p := range parts {
		h := &zip.FileHeader{
			Name:           p.name,
			ModifiedDate:   0x2101, // 1980-01-01, the DOS epoch
			CreatorVersion: 20,
			ReaderVersion:  20,
			Method:         zip.Deflate,
		}
		payload := p.body
		if path.Ext(p.name) == ".png" {
			h.Method = zip.Store
		} else {
			var buf bytes.Buffer
			fw, err := flate.NewWriter(&buf, flate.DefaultCompression)
			if err != nil {
				t.Fatalf("flate: %v", err)
			}
			if _, err := fw.Write(p.body); err != nil {
				t.Fatalf("flate write: %v", err)
			}
			if err := fw.Close(); err != nil {
				t.Fatalf("flate close: %v", err)
			}
			payload = buf.Bytes()
		}
		h.CRC32 = crc32.ChecksumIEEE(p.body)
		h.CompressedSize64 = uint64(len(payload))
		h.UncompressedSize64 = uint64(len(p.body))

		w, err := z.CreateRaw(h)
		if err != nil {
			t.Fatalf("CreateRaw %s: %v", p.name, err)
		}
		if _, err := w.Write(payload); err != nil {
			t.Fatalf("write %s: %v", p.name, err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}
	return out.Bytes()
}

// patchHeaders walks every central-directory record and its local header,
// handing both offsets to fn so a test can corrupt one field precisely.
func patchHeaders(t *testing.T, raw []byte, fn func(buf []byte, cd, local int)) []byte {
	t.Helper()
	buf := append([]byte(nil), raw...)
	i := bytes.LastIndex(buf, []byte("PK\x05\x06"))
	if i < 0 {
		t.Fatal("no EOCD")
	}
	count := int(binary.LittleEndian.Uint16(buf[i+10:]))
	offset := int(binary.LittleEndian.Uint32(buf[i+16:]))
	for n := 0; n < count; n++ {
		local := int(binary.LittleEndian.Uint32(buf[offset+42:]))
		fn(buf, offset, local)
		nameLen := int(binary.LittleEndian.Uint16(buf[offset+28:]))
		extraLen := int(binary.LittleEndian.Uint16(buf[offset+30:]))
		commentLen := int(binary.LittleEndian.Uint16(buf[offset+32:]))
		offset += 46 + nameLen + extraLen + commentLen
	}
	return buf
}

func rules(findings []pptx.Finding) map[string]int {
	out := map[string]int{}
	for _, f := range findings {
		out[f.Rule]++
	}
	return out
}

func verifyBytes(t *testing.T, raw []byte) []pptx.Finding {
	t.Helper()
	findings, err := pptx.VerifyBytes(raw)
	if err != nil {
		t.Fatalf("VerifyBytes: %v", err)
	}
	return findings
}

// ── the control: a good package must be silent ──────────────────────────────

func TestVerifyAcceptsWhatTheWriterEmits(t *testing.T) {
	if findings := verifyBytes(t, goodPackage(t)); len(findings) > 0 {
		for _, f := range findings {
			t.Errorf("unexpected finding: %s", f)
		}
	}
}

func TestVerifyAcceptsARebuiltPackage(t *testing.T) {
	// Guards the test machinery itself: if rezip did not mirror add(), every
	// package-level fixture below would carry extra findings and the
	// single-rule assertions would be meaningless.
	raw := rezip(t, explode(t, goodPackage(t)))
	if findings := verifyBytes(t, raw); len(findings) > 0 {
		for _, f := range findings {
			t.Errorf("rezip does not mirror add(): %s", f)
		}
	}
}

// ── every rule must fire ────────────────────────────────────────────────────

// TestVerifyCatchesEveryDefect is this package's check-selftest: a verifier
// that silently stopped enforcing a rule would look exactly like a clean
// build. Each case corrupts one thing and requires that one rule.
func TestVerifyCatchesEveryDefect(t *testing.T) {
	cases := []struct {
		rule    string
		corrupt func(t *testing.T, raw []byte) []byte
	}{{
		// The defect shipped in #21.
		rule: pptx.RuleZipVersion,
		corrupt: func(t *testing.T, raw []byte) []byte {
			return patchHeaders(t, raw, func(b []byte, cd, local int) {
				binary.LittleEndian.PutUint16(b[cd+4:], 0)
				binary.LittleEndian.PutUint16(b[cd+6:], 0)
				binary.LittleEndian.PutUint16(b[local+4:], 0)
			})
		},
	}, {
		// The defect shipped in #20.
		rule: pptx.RuleZipModDate,
		corrupt: func(t *testing.T, raw []byte) []byte {
			return patchHeaders(t, raw, func(b []byte, cd, local int) {
				binary.LittleEndian.PutUint16(b[cd+14:], 0)
				binary.LittleEndian.PutUint16(b[local+10:], 0)
			})
		},
	}, {
		// The streaming shape the writer avoids.
		rule: pptx.RuleDataDescriptor,
		corrupt: func(t *testing.T, raw []byte) []byte {
			return patchHeaders(t, raw, func(b []byte, cd, local int) {
				binary.LittleEndian.PutUint16(b[cd+8:], 0x0008)
			})
		},
	}, {
		rule: pptx.RuleEncrypted,
		corrupt: func(t *testing.T, raw []byte) []byte {
			return patchHeaders(t, raw, func(b []byte, cd, local int) {
				binary.LittleEndian.PutUint16(b[cd+8:], 0x0001)
			})
		},
	}, {
		rule: pptx.RuleZipSizes,
		corrupt: func(t *testing.T, raw []byte) []byte {
			return patchHeaders(t, raw, func(b []byte, cd, local int) {
				binary.LittleEndian.PutUint32(b[local+14:], 0) // local CRC only
			})
		},
	}, {
		rule: pptx.RuleContentTypesFirst,
		corrupt: func(t *testing.T, raw []byte) []byte {
			parts := explode(t, raw)
			parts[0], parts[1] = parts[1], parts[0]
			return rezip(t, parts)
		},
	}, {
		rule: pptx.RuleEntryName,
		corrupt: func(t *testing.T, raw []byte) []byte {
			parts := explode(t, raw)
			return rezip(t, append(parts, parts[1]))
		},
	}, {
		rule: pptx.RuleContentType,
		corrupt: func(t *testing.T, raw []byte) []byte {
			parts := explode(t, raw)
			for i := range parts {
				if parts[i].name == "[Content_Types].xml" {
					parts[i].body = []byte(strings.Replace(string(parts[i].body),
						`<Override PartName="/ppt/theme/theme2.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/>`,
						"", 1))
				}
			}
			return rezip(t, parts)
		},
	}, {
		rule: pptx.RuleXMLWellFormed,
		corrupt: func(t *testing.T, raw []byte) []byte {
			parts := explode(t, raw)
			for i := range parts {
				if parts[i].name == "ppt/slides/slide1.xml" {
					parts[i].body = parts[i].body[:len(parts[i].body)/2]
				}
			}
			return rezip(t, parts)
		},
	}, {
		rule: pptx.RuleRelationshipTarget,
		corrupt: func(t *testing.T, raw []byte) []byte {
			parts := explode(t, raw)
			for i := range parts {
				if parts[i].name == "ppt/slides/_rels/slide1.xml.rels" {
					parts[i].body = []byte(strings.Replace(string(parts[i].body),
						"../media/slide1.png", "../media/absent.png", 1))
				}
			}
			return rezip(t, parts)
		},
	}, {
		rule: pptx.RuleRelationshipID,
		corrupt: func(t *testing.T, raw []byte) []byte {
			parts := explode(t, raw)
			for i := range parts {
				if parts[i].name == "ppt/slides/_rels/slide1.xml.rels" {
					// rId3 is the slide's image, which the slide XML
					// references with r:embed. Renaming rId2 (the notes
					// slide) would not fire: nothing inside slide1.xml
					// refers to it.
					parts[i].body = []byte(strings.Replace(string(parts[i].body),
						`Id="rId3"`, `Id="rIdGone"`, 1))
				}
			}
			return rezip(t, parts)
		},
	}, {
		rule: pptx.RuleSlideIDList,
		corrupt: func(t *testing.T, raw []byte) []byte {
			parts := explode(t, raw)
			for i := range parts {
				if parts[i].name == "ppt/presentation.xml" {
					parts[i].body = []byte(strings.Replace(string(parts[i].body),
						`<p:sldId id="257" r:id="rId3"/>`, "", 1))
				}
			}
			return rezip(t, parts)
		},
	}, {
		// The defect shipped in #23.
		rule: pptx.RuleThemePerMaster,
		corrupt: func(t *testing.T, raw []byte) []byte {
			parts := explode(t, raw)
			for i := range parts {
				if parts[i].name == "ppt/notesMasters/_rels/notesMaster1.xml.rels" {
					parts[i].body = []byte(strings.Replace(string(parts[i].body),
						"../theme/theme2.xml", "../theme/theme1.xml", 1))
				}
			}
			return rezip(t, parts)
		},
	}, {
		rule: pptx.RuleSlideSize,
		corrupt: func(t *testing.T, raw []byte) []byte {
			parts := explode(t, raw)
			for i := range parts {
				if parts[i].name == "ppt/presentation.xml" {
					parts[i].body = []byte(strings.Replace(string(parts[i].body),
						`cy="6858000"/>`+"\n", `cy="0"/>`+"\n", 1))
				}
			}
			return rezip(t, parts)
		},
	}, {
		rule: pptx.RuleMediaIntegrity,
		corrupt: func(t *testing.T, raw []byte) []byte {
			parts := explode(t, raw)
			for i := range parts {
				if parts[i].name == "ppt/media/slide1.png" {
					parts[i].body = parts[i].body[:len(parts[i].body)-8]
				}
			}
			return rezip(t, parts)
		},
	}}

	good := goodPackage(t)
	for _, tc := range cases {
		t.Run(tc.rule, func(t *testing.T) {
			findings := verifyBytes(t, tc.corrupt(t, good))
			if rules(findings)[tc.rule] == 0 {
				t.Errorf("corrupting for %q produced no such finding; got %v", tc.rule, rules(findings))
				for _, f := range findings {
					t.Logf("  %s", f)
				}
			}
		})
	}
}

// ── Write refuses to ship a package that fails ──────────────────────────────

func TestWriteRefusesToShipAnInvalidPackage(t *testing.T) {
	// Verify runs before the rename, so a failing package must leave nothing
	// behind at the destination -- the same guarantee the temp-file dance
	// already gives for a write error.
	dir := t.TempDir()
	dst := filepath.Join(dir, "deck.pptx")

	// A deck whose PNG is not a PNG: media-integrity must fire, and the file
	// must not appear.
	err := pptx.Write(dst, pptx.Deck{
		WidthIn:  13.333,
		HeightIn: 7.5,
		Slides:   []pptx.Slide{{PNG: []byte("not a png at all")}},
	})
	if err == nil {
		t.Fatal("Write accepted a deck whose image is not a PNG")
	}
	var fe *pptx.FindingsError
	if !errors.As(err, &fe) {
		t.Fatalf("want a *pptx.FindingsError, got %T: %v", err, err)
	}
	if rules(fe.Findings)[pptx.RuleMediaIntegrity] == 0 {
		t.Errorf("want a media-integrity finding, got %v", rules(fe.Findings))
	}
	if _, statErr := os.Stat(dst); statErr == nil {
		t.Error("an invalid package was left at the destination")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		t.Errorf("left behind: %s", e.Name())
	}
}

// ── the findings a human reads ──────────────────────────────────────────────

func TestFindingStringNamesThePartWhenThereIsOne(t *testing.T) {
	withPart := pptx.Finding{Rule: pptx.RuleZipVersion, Part: "ppt/slides/slide1.xml", Detail: "declares 0"}
	if got, want := withPart.String(), "zip-version: ppt/slides/slide1.xml: declares 0"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}

	// A package-level finding has no part, and must not render an empty one --
	// "zip-version: : ..." reads like a bug in the reporter.
	packageWide := pptx.Finding{Rule: pptx.RuleEntryCount, Detail: "the package has no entries"}
	if got, want := packageWide.String(), "entry-count: the package has no entries"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestFindingsErrorListsEveryFinding(t *testing.T) {
	err := &pptx.FindingsError{
		Path: "/out/deck.pptx",
		Findings: []pptx.Finding{
			{Rule: pptx.RuleThemePerMaster, Part: "ppt/theme/theme1.xml", Detail: "shared by 2 masters"},
			{Rule: pptx.RuleMediaIntegrity, Part: "ppt/media/slide1.png", Detail: "is not a complete PNG"},
		},
	}
	msg := err.Error()
	for _, want := range []string{
		"/out/deck.pptx", "2 finding(s)",
		"theme-per-master: ppt/theme/theme1.xml: shared by 2 masters",
		"media-integrity: ppt/media/slide1.png: is not a complete PNG",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("Error() is missing %q:\n%s", want, msg)
		}
	}
}

// ── error paths ─────────────────────────────────────────────────────────────

func TestVerifyReportsAnUnreadableFile(t *testing.T) {
	if _, err := pptx.Verify(filepath.Join(t.TempDir(), "absent.pptx")); err == nil {
		t.Error("want an error for a file that does not exist")
	}
}

func TestVerifyBytesRejectsSomethingThatIsNotAZip(t *testing.T) {
	// An error, not a finding: without a central directory there is no package
	// to report findings about, and saying "0 findings" would read as a pass.
	if _, err := pptx.VerifyBytes([]byte("this is not a zip archive")); err == nil {
		t.Error("want an error for bytes with no end-of-central-directory record")
	}
}

func TestVerifyReportsAPackageWithNoContentTypeMap(t *testing.T) {
	parts := explode(t, goodPackage(t))
	kept := parts[:0]
	for _, p := range parts {
		if p.name != "[Content_Types].xml" {
			kept = append(kept, p)
		}
	}
	findings := verifyBytes(t, rezip(t, kept))
	if rules(findings)[pptx.RuleContentType] == 0 {
		t.Errorf("want a content-type finding, got %v", rules(findings))
	}
}

func TestVerifyReportsAPackageWithNoPresentationPart(t *testing.T) {
	parts := explode(t, goodPackage(t))
	kept := parts[:0]
	for _, p := range parts {
		if p.name != "ppt/presentation.xml" {
			kept = append(kept, p)
		}
	}
	findings := verifyBytes(t, rezip(t, kept))
	if rules(findings)[pptx.RuleSlideIDList] == 0 {
		t.Errorf("want a slide-id-list finding, got %v", rules(findings))
	}
}

func TestVerifyReportsAMasterWithNoTheme(t *testing.T) {
	parts := explode(t, goodPackage(t))
	for i := range parts {
		if parts[i].name == "ppt/notesMasters/_rels/notesMaster1.xml.rels" {
			parts[i].body = []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
				`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"></Relationships>`)
		}
	}
	findings := verifyBytes(t, rezip(t, parts))
	if rules(findings)[pptx.RuleThemePerMaster] == 0 {
		t.Errorf("want a theme-per-master finding for a master with no theme, got %v", rules(findings))
	}
}

func TestVerifyReportsASlideListedButMissing(t *testing.T) {
	parts := explode(t, goodPackage(t))
	kept := parts[:0]
	for _, p := range parts {
		if p.name != "ppt/slides/slide2.xml" {
			kept = append(kept, p)
		}
	}
	findings := verifyBytes(t, rezip(t, kept))
	if rules(findings)[pptx.RuleSlideIDList] == 0 {
		t.Errorf("want a slide-id-list finding for a listed-but-absent slide, got %v", rules(findings))
	}
}

// ── the remaining rules, each proven rather than asserted ───────────────────

// mutate returns the package with one part's body rewritten by fn.
func mutate(t *testing.T, raw []byte, name string, fn func(string) string) []byte {
	t.Helper()
	parts := explode(t, raw)
	found := false
	for i := range parts {
		if parts[i].name == name {
			parts[i].body = []byte(fn(string(parts[i].body)))
			found = true
		}
	}
	if !found {
		t.Fatalf("no such part to mutate: %s", name)
	}
	return rezip(t, parts)
}

func TestVerifyRejectsAnUnusableEntryName(t *testing.T) {
	good := goodPackage(t)
	for _, tc := range []struct{ label, name string }{
		{"backslash", `ppt\slides\slide1.xml`},
		{"absolute", "/ppt/slides/slide1.xml"},
		{"traversal", "ppt/slides/../../../etc/passwd"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			parts := explode(t, good)
			for i := range parts {
				if parts[i].name == "ppt/slides/slide1.xml" {
					parts[i].name = tc.name
				}
			}
			findings := verifyBytes(t, rezip(t, parts))
			if rules(findings)[pptx.RuleEntryName] == 0 {
				t.Errorf("%s name produced no entry-name finding; got %v", tc.label, rules(findings))
			}
		})
	}
}

func TestVerifyRejectsAnEmptyPackage(t *testing.T) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	if err := z.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	findings := verifyBytes(t, buf.Bytes())
	if rules(findings)[pptx.RuleEntryCount] == 0 {
		t.Errorf("want an entry-count finding for a package with no entries, got %v", rules(findings))
	}
}

func TestVerifyRejectsAWrongContentType(t *testing.T) {
	// Not merely missing -- present and wrong. A reader dispatching on content
	// type would not know it is looking at a slide.
	raw := mutate(t, goodPackage(t), "[Content_Types].xml", func(s string) string {
		return strings.Replace(s,
			`PartName="/ppt/slides/slide1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"`,
			`PartName="/ppt/slides/slide1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.notesSlide+xml"`,
			1)
	})
	findings := verifyBytes(t, raw)
	if rules(findings)[pptx.RuleContentType] == 0 {
		t.Errorf("want a content-type finding for a part typed as the wrong thing, got %v", rules(findings))
	}
}

func TestVerifyRejectsAPartWithNoDefaultAndNoOverride(t *testing.T) {
	parts := append(explode(t, goodPackage(t)), namedPart{"ppt/embeddings/oleObject1.bin", []byte("payload")})
	findings := verifyBytes(t, rezip(t, parts))
	if rules(findings)[pptx.RuleContentType] == 0 {
		t.Errorf("want a content-type finding for an untypeable extension, got %v", rules(findings))
	}
}

func TestVerifyReportsAMalformedContentTypeMap(t *testing.T) {
	raw := mutate(t, goodPackage(t), "[Content_Types].xml", func(s string) string { return s[:len(s)/2] })
	findings := verifyBytes(t, raw)
	if rules(findings)[pptx.RuleContentType] == 0 {
		t.Errorf("want a content-type finding for an unparsable map, got %v", rules(findings))
	}
}

func TestVerifyReportsAMalformedPresentationPart(t *testing.T) {
	raw := mutate(t, goodPackage(t), "ppt/presentation.xml", func(s string) string {
		return strings.Replace(s, "</p:presentation>", "", 1)
	})
	findings := verifyBytes(t, raw)
	if rules(findings)[pptx.RuleXMLWellFormed] == 0 {
		t.Errorf("want an xml-well-formed finding, got %v", rules(findings))
	}
}

func TestVerifyRejectsABadSlideIDList(t *testing.T) {
	good := goodPackage(t)
	for _, tc := range []struct {
		label string
		from  string
		to    string
	}{
		{"duplicate id", `<p:sldId id="257" r:id="rId3"/>`, `<p:sldId id="256" r:id="rId3"/>`},
		{"id below the floor", `<p:sldId id="256"`, `<p:sldId id="12"`},
		{"id above the ceiling", `<p:sldId id="256"`, `<p:sldId id="2147483648"`},
		{"dangling relationship", `r:id="rId2"/>`, `r:id="rIdMissing"/>`},
	} {
		t.Run(tc.label, func(t *testing.T) {
			raw := mutate(t, good, "ppt/presentation.xml", func(s string) string {
				out := strings.Replace(s, tc.from, tc.to, 1)
				if out == s {
					t.Fatalf("fixture did not apply: %q not found", tc.from)
				}
				return out
			})
			findings := verifyBytes(t, raw)
			if rules(findings)[pptx.RuleSlideIDList] == 0 {
				t.Errorf("%s produced no slide-id-list finding; got %v", tc.label, rules(findings))
			}
		})
	}
}

func TestVerifyIgnoresAnExternalRelationshipTarget(t *testing.T) {
	// An External target names a URL, not a part, so resolving it against the
	// package would report every hyperlink as missing.
	raw := mutate(t, goodPackage(t), "ppt/slides/_rels/slide1.xml.rels", func(s string) string {
		return strings.Replace(s, "</Relationships>",
			`<Relationship Id="rIdLink" `+
				`Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink" `+
				`Target="https://example.invalid/" TargetMode="External"/></Relationships>`, 1)
	})
	findings := verifyBytes(t, raw)
	if n := rules(findings)[pptx.RuleRelationshipTarget]; n != 0 {
		t.Errorf("an External target was reported as missing (%d finding(s))", n)
		for _, f := range findings {
			t.Logf("  %s", f)
		}
	}
}

// ── native (editable) slides ────────────────────────────────────────────────

// nativeDeck exercises every shape kind on one slide.
func nativeDeck() pptx.Deck {
	return pptx.Deck{
		WidthIn:  13.333,
		HeightIn: 7.5,
		Lang:     "pt-BR",
		Slides: []pptx.Slide{{
			Notes: "speaker notes survive native mode too",
			Shapes: []pptx.Shape{
				{Kind: pptx.ShapeRect, At: pptx.Rect{XIn: 0, YIn: 0, WIn: 13.333, HIn: 7.5}, Fill: "FBF8F1"},
				{Kind: pptx.ShapeRect, At: pptx.Rect{XIn: 0.6, YIn: 1.9, WIn: 0.03, HIn: 0.5}, Fill: "02ACCF"},
				{Kind: pptx.ShapeText, At: pptx.Rect{XIn: 0.6, YIn: 0.5, WIn: 11, HIn: 0.4},
					Text: "VISÃO GERAL", Style: pptx.TextStyle{SizePt: 11, Color: "0E3C45", Caps: true, SpacingPt: 1.5, Font: "Roboto"}},
				{Kind: pptx.ShapeText, At: pptx.Rect{XIn: 0.6, YIn: 1.0, WIn: 11, HIn: 0.9},
					Text: "Linha um\nLinha dois", Style: pptx.TextStyle{SizePt: 28, Bold: true, Color: "343B43", Font: "Roboto"}},
				{Kind: pptx.ShapeBullets, At: pptx.Rect{XIn: 0.6, YIn: 2.5, WIn: 6, HIn: 3},
					Items: []string{"primeiro", "segundo", "terceiro"},
					Style: pptx.TextStyle{SizePt: 16, Color: "343B43", LinePct: 120}},
				{Kind: pptx.ShapeTable, At: pptx.Rect{XIn: 7, YIn: 2.5, WIn: 5.7, HIn: 3},
					Columns: []string{"Clube", "Sócios"},
					Rows:    [][]string{{"Clube Ômega", "9,8 milhões"}, {"Clube Zêta", "7,1 milhões"}},
					Fill:    "02ACCF",
					Style:   pptx.TextStyle{SizePt: 12, Color: "343B43"}},
				{Kind: pptx.ShapePicture, At: pptx.Rect{XIn: 11.8, YIn: 0.4, WIn: 1, HIn: 1}, PNG: onePixelPNG},
			},
		}},
	}
}

func TestNativeSlidePassesVerification(t *testing.T) {
	// The whole feature rests on this: shapes that a strict reader rejects
	// would be worse than the flat image they replace.
	dst := filepath.Join(t.TempDir(), "native.pptx")
	if err := pptx.Write(dst, nativeDeck()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	findings, err := pptx.Verify(dst)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	for _, f := range findings {
		t.Errorf("native slide produced a finding: %s", f)
	}
}

func TestNativeSlideEmitsRealPowerPointObjects(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "native.pptx")
	if err := pptx.Write(dst, nativeDeck()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	parts, _ := readAll(t, dst)
	slide := parts["ppt/slides/slide1.xml"]

	for _, want := range []struct{ what, xml string }{
		{"a real table, not text boxes", "<a:tbl>"},
		{"table grid columns", "<a:gridCol"},
		{"a bulleted list", "<a:buChar"},
		{"an editable text box", `txBox="1"`},
		{"a picture", "<p:blipFill>"},
		{"the deck's language on runs", `lang="pt-BR"`},
		{"theme colour on text", "343B43"},
		{"OOXML capitalisation rather than upper-cased source", `cap="all"`},
	} {
		if !strings.Contains(slide, want.xml) {
			t.Errorf("native slide is missing %s (%s)", want.what, want.xml)
		}
	}

	// Caps must not have rewritten the author's text.
	if !strings.Contains(slide, "VISÃO GERAL") {
		t.Error("kicker text is missing")
	}
	// A native slide carries no full-bleed image.
	if strings.Contains(slide, `name="Slide 1"`) {
		t.Error("a native slide still emitted the image-mode full-bleed picture")
	}
	// Its picture must resolve.
	if !strings.Contains(parts["ppt/slides/_rels/slide1.xml.rels"], "slide1-1.png") {
		t.Error("the native slide's picture has no image relationship")
	}
}

func TestImageModeIsUnchangedByNativeSupport(t *testing.T) {
	// Native mode must not have perturbed the flat-image path: that is what
	// ships to anyone who wants exact fidelity.
	parts, _ := readAll(t, writeDeck(t, pptx.Slide{PNG: onePixelPNG, Notes: "x"}))
	slide := parts["ppt/slides/slide1.xml"]
	if !strings.Contains(slide, `name="Slide 1"`) {
		t.Error("image mode no longer emits the full-bleed picture")
	}
	if strings.Contains(slide, "<a:tbl>") || strings.Contains(slide, `txBox="1"`) {
		t.Error("image mode emitted native shapes")
	}
	if _, ok := parts["ppt/media/slide1.png"]; !ok {
		t.Error("image mode's media part was renamed")
	}
}

// ── table geometry ──────────────────────────────────────────────────────────

func TestTableUsesMeasuredRowHeights(t *testing.T) {
	// Even division was the defect: a header and a two-line body row are not
	// the same height, and dividing the frame evenly put every table 14-18%
	// off the reference render.
	dst := filepath.Join(t.TempDir(), "t.pptx")
	err := pptx.Write(dst, pptx.Deck{
		WidthIn: 13.333, HeightIn: 7.5, Lang: "pt-BR",
		Slides: []pptx.Slide{{Shapes: []pptx.Shape{{
			Kind:         pptx.ShapeTable,
			At:           pptx.Rect{XIn: 0.625, YIn: 2, WIn: 12, HIn: 1.5},
			Columns:      []string{"Clube", "Sócios"},
			Rows:         [][]string{{"Clube Ômega", "9,8 milhões"}, {"Clube Zêta", "7,1 milhões"}},
			RowHeightsIn: []float64{0.5, 0.4, 0.6},
			ColWidthsIn:  []float64{4, 8},
			Style:        pptx.TextStyle{SizePt: 12},
		}}}},
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	parts, _ := readAll(t, dst)
	slide := parts["ppt/slides/slide1.xml"]

	// 0.5in, 0.4in, 0.6in as EMU.
	for _, want := range []string{`h="457200"`, `h="365760"`, `h="548640"`} {
		if !strings.Contains(slide, want) {
			t.Errorf("missing measured row height %s", want)
		}
	}
	// Columns: 4in and 8in.
	for _, want := range []string{`w="3657600"`, `w="7315200"`} {
		if !strings.Contains(slide, want) {
			t.Errorf("missing measured column width %s", want)
		}
	}
}

func TestTableGridAlwaysTotalsItsFrame(t *testing.T) {
	// A grid that does not sum to its own xfrm makes PowerPoint re-flow the
	// table, which would silently undo the measurement.
	dst := filepath.Join(t.TempDir(), "t.pptx")
	err := pptx.Write(dst, pptx.Deck{
		WidthIn: 13.333, HeightIn: 7.5,
		Slides: []pptx.Slide{{Shapes: []pptx.Shape{{
			Kind: pptx.ShapeTable,
			At:   pptx.Rect{XIn: 0, YIn: 0, WIn: 10, HIn: 3},
			Rows: [][]string{{"a", "b", "c"}, {"d", "e", "f"}},
			// Deliberately short of the 10in frame.
			ColWidthsIn: []float64{3.3, 3.3, 3.3},
		}}}},
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	parts, _ := readAll(t, dst)
	slide := parts["ppt/slides/slide1.xml"]

	var sum int64
	for _, m := range regexp.MustCompile(`<a:gridCol w="(\d+)"/>`).FindAllStringSubmatch(slide, -1) {
		v, _ := strconv.ParseInt(m[1], 10, 64)
		sum += v
	}
	if want := int64(10 * 914400); sum != want {
		t.Errorf("grid columns total %d EMU, want exactly the frame's %d", sum, want)
	}
}

func TestEveryNativeTextBoxDeclaresAnAutofit(t *testing.T) {
	// The cover that prompted this overflowed its title box into the subtitle
	// because nothing told PowerPoint what to do when the text did not fit.
	dst := filepath.Join(t.TempDir(), "native.pptx")
	if err := pptx.Write(dst, nativeDeck()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	parts, _ := readAll(t, dst)
	slide := parts["ppt/slides/slide1.xml"]
	if !strings.Contains(slide, "<a:normAutofit/>") {
		t.Error("native text boxes do not declare normAutofit")
	}
	findings, err := pptx.Verify(dst)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	for _, f := range findings {
		t.Errorf("unexpected finding: %s", f)
	}
}

func TestVerifyCatchesATextBoxWithNoAutofit(t *testing.T) {
	parts := explode(t, mustBytes(t, nativeDeck()))
	for i := range parts {
		if parts[i].name == "ppt/slides/slide1.xml" {
			parts[i].body = []byte(strings.ReplaceAll(string(parts[i].body),
				"<a:normAutofit/>", ""))
		}
	}
	findings := verifyBytes(t, rezip(t, parts))
	if rules(findings)[pptx.RuleTextAutofit] == 0 {
		t.Errorf("want a text-autofit finding, got %v", rules(findings))
	}
}

func TestARectangleNeedsNoAutofit(t *testing.T) {
	// A rectangle carries an empty txBody because the schema wants one; it can
	// never overflow, and flagging it would be noise on every native slide.
	dst := filepath.Join(t.TempDir(), "rect.pptx")
	err := pptx.Write(dst, pptx.Deck{
		WidthIn: 13.333, HeightIn: 7.5,
		Slides: []pptx.Slide{{Shapes: []pptx.Shape{
			{Kind: pptx.ShapeRect, At: pptx.Rect{WIn: 13.333, HIn: 7.5}, Fill: "FBF8F1"},
		}}},
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	findings, err := pptx.Verify(dst)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	for _, f := range findings {
		t.Errorf("a rectangle produced a finding: %s", f)
	}
}
