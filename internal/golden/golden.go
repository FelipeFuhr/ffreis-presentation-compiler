// Package golden implements perceptual visual-regression comparison for the
// PNG slides the compiler renders.
//
// Chrome's PNG encoder is not byte-reproducible across versions — subpixel
// rounding and anti-aliasing shift by a few intensity levels per channel on
// every release — so comparing encoded bytes would fail on every Chrome
// update and teach everyone to re-approve the baseline without looking.
// Comparing decoded pixels with a small per-pixel tolerance survives that
// noise while still catching an actual visual change: a moved box or a
// recoloured accent differs by far more, over far more of the slide, than
// rendering noise ever does.
package golden

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// PixelDiffThreshold is the combined per-channel difference (R+G+B+A, each
// 0-255, so 0-1020 total) above which two pixels count as visually
// different rather than rendering noise. Chrome's anti-aliasing shifts a
// handful of levels per channel between versions; a real content change —
// a recoloured accent, a moved box — differs by an order of magnitude more
// than that across its own area.
const PixelDiffThreshold = 24

// DefaultDiffRatioTolerance is the fraction of a slide's pixels allowed to
// differ before a mismatch counts as a regression rather than noise. 1%
// comfortably survives a Chrome version bump's anti-aliasing drift (which
// measures near zero on an unmodified theme, see TestCompareDirPasses...)
// while still catching a one-line theme change, which recolours or moves a
// meaningful fraction of a slide's area.
const DefaultDiffRatioTolerance = 0.01

// diffHighlight is painted over a differing pixel in a diff artifact: fully
// opaque red, chosen to stand out against any theme's palette.
var diffHighlight = color.NRGBA{R: 255, A: 255}

// SlideResult is one slide's comparison outcome.
type SlideResult struct {
	// Name is the baseline's filename, e.g. "01.png".
	Name            string
	BaselineSize    image.Point
	CandidateSize   image.Point
	DifferingPixels int
	TotalPixels     int
	// DiffPath is where a highlight artifact was written, set only when the
	// slide mismatched and a diff directory was given.
	DiffPath string
}

// DimensionsMatch reports whether the baseline and candidate share a size.
// A pixel ratio is meaningless when they do not.
func (r SlideResult) DimensionsMatch() bool {
	return r.BaselineSize == r.CandidateSize
}

// Ratio is the fraction of pixels that differ. It is only meaningful when
// DimensionsMatch is true.
func (r SlideResult) Ratio() float64 {
	if r.TotalPixels == 0 {
		return 0
	}
	return float64(r.DifferingPixels) / float64(r.TotalPixels)
}

// Mismatch reports whether this slide fails tolerance. A dimension mismatch
// always fails regardless of tolerance — no pixel ratio can describe two
// different-shaped images, so it is a hard failure rather than a 100% diff.
func (r SlideResult) Mismatch(tolerance float64) bool {
	if !r.DimensionsMatch() {
		return true
	}
	return r.Ratio() > tolerance
}

// CompareDir compares every baseline PNG in baselineDir against the
// same-named file in candidateDir, writing a highlight artifact for any
// mismatch into diffDir (skipped when diffDir is empty). Results are
// ordered by filename, so output is stable across runs.
func CompareDir(baselineDir, candidateDir, diffDir string, tolerance float64) ([]SlideResult, error) {
	names, err := baselineNames(baselineDir)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("golden: no baseline PNGs found in %s", baselineDir)
	}

	results := make([]SlideResult, 0, len(names))
	for _, name := range names {
		result, err := compareOne(baselineDir, candidateDir, diffDir, name, tolerance)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

// baselineNames lists the approved baseline's PNG filenames, sorted.
func baselineNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("golden: read baseline %s: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".png") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// compareOne compares one baseline/candidate pair. A missing or unreadable
// file on either side is a structural problem, not a content diff, so it is
// a hard error rather than part of the result.
func compareOne(baselineDir, candidateDir, diffDir, name string, tolerance float64) (SlideResult, error) {
	baseline, err := decodePNG(filepath.Join(baselineDir, name))
	if err != nil {
		return SlideResult{}, fmt.Errorf("golden: baseline %s: %w", name, err)
	}
	candidate, err := decodePNG(filepath.Join(candidateDir, name))
	if err != nil {
		return SlideResult{}, fmt.Errorf("golden: candidate %s: %w", name, err)
	}

	result := SlideResult{
		Name:          name,
		BaselineSize:  baseline.Bounds().Size(),
		CandidateSize: candidate.Bounds().Size(),
	}
	if !result.DimensionsMatch() {
		return result, nil
	}

	differing, total, diff := compareImages(baseline, candidate)
	result.DifferingPixels = differing
	result.TotalPixels = total
	if float64(differing)/float64(total) <= tolerance || diffDir == "" {
		return result, nil
	}

	path, err := writeDiffImage(diffDir, name, diff)
	if err != nil {
		return SlideResult{}, fmt.Errorf("golden: write diff for %s: %w", name, err)
	}
	result.DiffPath = path
	return result, nil
}

func decodePNG(path string) (image.Image, error) {
	f, err := os.Open(path) //nolint:gosec // path is assembled from an operator-supplied baseline/candidate directory, same trust boundary as -dir elsewhere
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only handle; a close error cannot lose data

	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return img, nil
}

// compareImages walks both images pixel by pixel, counting how many differ
// by more than PixelDiffThreshold and building a highlight image in the same
// pass: the differing pixels in solid red, everything else dimmed to grey so
// the highlight stands out against the slide's own content.
func compareImages(baseline, candidate image.Image) (differing, total int, diff *image.NRGBA) {
	bounds := baseline.Bounds()
	diff = image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			total++
			if pixelDiffers(baseline.At(x, y), candidate.At(x, y)) {
				differing++
				diff.SetNRGBA(x, y, diffHighlight)
				continue
			}
			diff.SetNRGBA(x, y, dimmed(candidate.At(x, y)))
		}
	}
	return differing, total, diff
}

// dimmed converts c to a darkened grey so a passing region of the diff
// artifact reads as context, not signal.
func dimmed(c color.Color) color.NRGBA {
	g, _ := color.GrayModel.Convert(c).(color.Gray) // GrayModel always converts to Gray
	v := g.Y / 3
	return color.NRGBA{R: v, G: v, B: v, A: 255}
}

// pixelDiffers reports whether a and b differ by more than
// PixelDiffThreshold, summed across all four channels.
func pixelDiffers(a, b color.Color) bool {
	ar, ag, ab, aa := rgba255(a)
	br, bg, bb, ba := rgba255(b)
	sum := absInt(ar-br) + absInt(ag-bg) + absInt(ab-bb) + absInt(aa-ba)
	return sum > PixelDiffThreshold
}

// rgba255 reduces a colour to 8-bit-per-channel components. color.Color's own
// RGBA() returns alpha-premultiplied 16-bit values; for the opaque screenshots
// this package compares that premultiplication is a no-op, so a plain shift
// is enough without reaching for a slower, more general colour conversion.
func rgba255(c color.Color) (r, g, b, a int) {
	cr, cg, cb, ca := c.RGBA()
	return int(cr >> 8), int(cg >> 8), int(cb >> 8), int(ca >> 8)
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// writeDiffImage encodes diff under dir, named after the slide's own
// filename, and returns the path written.
func writeDiffImage(dir, name string, diff *image.NRGBA) (string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	path := filepath.Join(dir, stem+".diff.png")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // diffDir is an operator-supplied flag, same trust boundary as -out elsewhere
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	if err := png.Encode(f, diff); err != nil {
		return "", err
	}
	return path, nil
}

// HasMismatch reports whether any result fails tolerance.
func HasMismatch(results []SlideResult, tolerance float64) bool {
	for _, r := range results {
		if r.Mismatch(tolerance) {
			return true
		}
	}
	return false
}

// Report renders results as a human-readable summary, one line per slide,
// mirroring internal/check's Finding/Report shape so the two read alike.
func Report(results []SlideResult, tolerance float64) string {
	var b strings.Builder
	failed := 0
	for _, r := range results {
		if r.Mismatch(tolerance) {
			failed++
		}
		b.WriteString(resultLine(r, tolerance))
		b.WriteString("\n")
	}
	if failed == 0 {
		fmt.Fprintf(&b, "\ngolden: ok — %d slide(s) within tolerance %.1f%%\n", len(results), tolerance*100)
		return b.String()
	}
	fmt.Fprintf(&b, "\ngolden: %d of %d slide(s) exceed tolerance %.1f%%\n", failed, len(results), tolerance*100)
	return b.String()
}

// resultLine renders one slide's line. A dimension mismatch is reported by
// name, not as a fabricated 100% diff — the two sizes are what a human needs
// to act on it.
func resultLine(r SlideResult, tolerance float64) string {
	label := slideLabel(r.Name)
	if !r.DimensionsMatch() {
		return fmt.Sprintf("%s: dimension mismatch — baseline %dx%d, candidate %dx%d",
			label, r.BaselineSize.X, r.BaselineSize.Y, r.CandidateSize.X, r.CandidateSize.Y)
	}

	status := "ok"
	if r.Mismatch(tolerance) {
		status = "FAIL"
	}
	line := fmt.Sprintf("%s: %s — %.1f%% of pixels differ, tolerance %.1f%%",
		label, status, r.Ratio()*100, tolerance*100)
	if r.DiffPath != "" {
		line += fmt.Sprintf(" (diff written to %s)", r.DiffPath)
	}
	return line
}

// slideLabel turns a baseline filename like "04.png" into "slide 4"; any
// other naming falls back to the raw filename rather than guessing.
func slideLabel(name string) string {
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	if n, err := strconv.Atoi(stem); err == nil {
		return fmt.Sprintf("slide %d", n)
	}
	return name
}
