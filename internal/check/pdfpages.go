package check

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
)

// RulePDFPages is the deck-level rule reported when a PDF's page count does
// not match the number of rendered slides.
const RulePDFPages = "pdf-pages"

// pageObjectPattern matches a PDF page object's /Type /Page entry, tolerating
// the no-space "/Type/Page" some writers emit. The trailing word boundary is
// the whole point of the pattern: it is what keeps this from also matching
// "/Type /Pages" (the page-tree node) — "e" and "s" are both word
// characters, so \b does not match between them, and the one false positive
// that would make this always overcount by at least one is avoided.
var pageObjectPattern = regexp.MustCompile(`/Type\s*/Page\b`)

// slideSectionPattern counts rendered [data-slide] sections directly in the
// HTML text — the same selector internal/engine/png.go captures by.
var slideSectionPattern = regexp.MustCompile(`data-slide="\d+"`)

// pdfHeader is the byte sequence every PDF starts with; its absence is what
// CountPDFPages treats as a malformed file rather than a page count of zero.
var pdfHeader = []byte("%PDF-")

// CountPDFPages counts /Type /Page objects in a PDF byte stream without a
// real parser: poppler (pdfinfo) may not be installed wherever this check
// runs, and a scan over the object dictionaries is enough to catch a
// silently dropped or doubled page. This will not see pages hidden inside a
// compressed cross-reference/object stream — accepted here the same way the
// rest of this package accepts false negatives are cheaper than a dependency
// on a real PDF library.
func CountPDFPages(data []byte) (int, error) {
	if !bytes.HasPrefix(data, pdfHeader) {
		return 0, fmt.Errorf("not a PDF: missing %q header", pdfHeader)
	}
	return len(pageObjectPattern.FindAll(data, -1)), nil
}

// CountSlideSections counts rendered [data-slide] sections in HTML text.
// This is deliberately not a browser measurement — pdf-pages must work even
// where Chrome never launches, the same reasoning that keeps CountPDFPages
// off of pdfinfo.
func CountSlideSections(html []byte) int {
	return len(slideSectionPattern.FindAll(html, -1))
}

// ComparePDFPages reads pdfPath and reports a Finding when its page count
// does not match slideCount. A nil Finding means no mismatch, not success —
// callers decide what a nil, nil result means for their own exit code.
func ComparePDFPages(pdfPath string, slideCount int) (*Finding, error) {
	data, err := os.ReadFile(pdfPath) //nolint:gosec // operator-supplied path, same trust boundary as -dir and -pdf
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", pdfPath, err)
	}
	pages, err := CountPDFPages(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", pdfPath, err)
	}
	if pages == slideCount {
		return nil, nil
	}
	return &Finding{
		Slide:   0,
		Rule:    RulePDFPages,
		Element: pdfPath,
		Detail:  fmt.Sprintf("pdf has %d page(s) but the deck has %d slide(s)", pages, slideCount),
	}, nil
}
