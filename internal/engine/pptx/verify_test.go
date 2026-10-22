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
					parts[i].body = []byte(strings.Replace(string(parts[i].body),
						`Id="rId2"`, `Id="rIdGone"`, 1))
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
