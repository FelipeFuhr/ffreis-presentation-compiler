package golden_test

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/golden"
)

// writeSolidPNG writes a w x h PNG filled with c at dir/name.
func writeSolidPNG(t *testing.T, dir, name string, w, h int, c color.Color) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	writePNG(t, dir, name, img)
}

func writePNG(t *testing.T, dir, name string, img image.Image) {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
}

func TestCompareDirPassesOnIdenticalImages(t *testing.T) {
	baseline, candidate := t.TempDir(), t.TempDir()
	blue := color.NRGBA{R: 37, G: 99, B: 235, A: 255}
	writeSolidPNG(t, baseline, "01.png", 20, 10, blue)
	writeSolidPNG(t, candidate, "01.png", 20, 10, blue)

	results, err := golden.CompareDir(baseline, candidate, "", golden.DefaultDiffRatioTolerance)
	if err != nil {
		t.Fatalf("CompareDir: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v, want exactly 1", results)
	}
	r := results[0]
	if r.Ratio() != 0 || r.Mismatch(golden.DefaultDiffRatioTolerance) {
		t.Errorf("identical images reported a mismatch: %+v", r)
	}
	if r.DiffPath != "" {
		t.Errorf("a passing slide must not write a diff artifact, got %q", r.DiffPath)
	}
}

func TestCompareDirToleratesAntiAliasingNoise(t *testing.T) {
	// Every channel shifts by 4, well under PixelDiffThreshold (24 summed
	// across 4 channels) — exactly the kind of drift a Chrome version bump
	// introduces, which must never fail the comparison.
	baseline, candidate := t.TempDir(), t.TempDir()
	writeSolidPNG(t, baseline, "01.png", 10, 10, color.NRGBA{R: 120, G: 120, B: 120, A: 255})
	writeSolidPNG(t, candidate, "01.png", 10, 10, color.NRGBA{R: 124, G: 124, B: 124, A: 255})

	results, err := golden.CompareDir(baseline, candidate, "", golden.DefaultDiffRatioTolerance)
	if err != nil {
		t.Fatalf("CompareDir: %v", err)
	}
	if results[0].DifferingPixels != 0 {
		t.Errorf("rendering noise below the threshold was counted as a diff: %+v", results[0])
	}
}

func TestCompareDirFlagsARealChange(t *testing.T) {
	baseline, candidate := t.TempDir(), t.TempDir()
	blue := color.NRGBA{R: 37, G: 99, B: 235, A: 255}
	writeSolidPNG(t, baseline, "04.png", 10, 10, blue)

	// Half the candidate image is a completely different colour — the shape
	// of an accent-colour change, far more drift than PixelDiffThreshold.
	img := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			if x < 5 {
				img.Set(x, y, color.NRGBA{R: 220, G: 38, B: 38, A: 255})
			} else {
				img.Set(x, y, blue)
			}
		}
	}
	writePNG(t, candidate, "04.png", img)

	diffDir := filepath.Join(t.TempDir(), "diffs")
	results, err := golden.CompareDir(baseline, candidate, diffDir, golden.DefaultDiffRatioTolerance)
	if err != nil {
		t.Fatalf("CompareDir: %v", err)
	}
	r := results[0]
	if !r.Mismatch(golden.DefaultDiffRatioTolerance) {
		t.Fatalf("a half-changed slide did not mismatch: %+v", r)
	}
	if r.Ratio() < 0.49 || r.Ratio() > 0.51 {
		t.Errorf("ratio = %v, want ~0.5", r.Ratio())
	}
	if r.DiffPath == "" {
		t.Fatal("a mismatching slide must write a diff artifact")
	}
	if info, err := os.Stat(r.DiffPath); err != nil || info.Size() == 0 {
		t.Errorf("diff artifact %s was not written: %v", r.DiffPath, err)
	}
}

func TestCompareDirDetectsDimensionMismatchWithoutFabricatingARatio(t *testing.T) {
	baseline, candidate := t.TempDir(), t.TempDir()
	writeSolidPNG(t, baseline, "02.png", 20, 10, color.White)
	writeSolidPNG(t, candidate, "02.png", 30, 10, color.White)

	// A tolerance of 1.0 (100%) must not rescue a dimension mismatch — it is
	// a hard failure regardless of tolerance, not a pixel-ratio question.
	results, err := golden.CompareDir(baseline, candidate, "", 1.0)
	if err != nil {
		t.Fatalf("CompareDir: %v", err)
	}
	r := results[0]
	if r.DimensionsMatch() {
		t.Fatal("want a dimension mismatch to be detected")
	}
	if !r.Mismatch(1.0) {
		t.Error("a dimension mismatch must fail even at 100% tolerance")
	}
	line := golden.Report(results, 1.0)
	for _, want := range []string{"slide 2", "dimension mismatch", "20x10", "30x10"} {
		if !strings.Contains(line, want) {
			t.Errorf("report %q does not mention %q", line, want)
		}
	}
}

func TestCompareDirErrorsOnAMissingCandidate(t *testing.T) {
	baseline, candidate := t.TempDir(), t.TempDir()
	writeSolidPNG(t, baseline, "01.png", 5, 5, color.White)

	if _, err := golden.CompareDir(baseline, candidate, "", golden.DefaultDiffRatioTolerance); err == nil {
		t.Fatal("want an error when the candidate has no matching file")
	}
}

func TestCompareDirErrorsOnAMissingBaselineDirectory(t *testing.T) {
	_, err := golden.CompareDir(filepath.Join(t.TempDir(), "absent"), t.TempDir(), "", golden.DefaultDiffRatioTolerance)
	if err == nil {
		t.Fatal("want an error when the baseline directory does not exist")
	}
}

func TestCompareDirErrorsOnAnEmptyBaselineDirectory(t *testing.T) {
	_, err := golden.CompareDir(t.TempDir(), t.TempDir(), "", golden.DefaultDiffRatioTolerance)
	if err == nil || !strings.Contains(err.Error(), "no baseline") {
		t.Fatalf("got %v, want an error naming the empty baseline", err)
	}
}

func TestReportSummarisesAllPassingAndAnyFailing(t *testing.T) {
	t.Run("every slide ok", func(t *testing.T) {
		results := []golden.SlideResult{
			{Name: "01.png", BaselineSize: image.Pt(10, 10), CandidateSize: image.Pt(10, 10), TotalPixels: 100},
		}
		got := golden.Report(results, 0.01)
		if !strings.Contains(got, "slide 1: ok") || !strings.Contains(got, "ok — 1 slide(s)") {
			t.Errorf("report = %q", got)
		}
	})

	t.Run("a failing slide names the percentage and the tolerance", func(t *testing.T) {
		results := []golden.SlideResult{
			{Name: "04.png", BaselineSize: image.Pt(10, 10), CandidateSize: image.Pt(10, 10),
				DifferingPixels: 31, TotalPixels: 1000, DiffPath: ".bin/golden/04.diff.png"},
		}
		got := golden.Report(results, 0.01)
		for _, want := range []string{"slide 4", "FAIL", "3.1% of pixels differ", "tolerance 1.0%", ".bin/golden/04.diff.png"} {
			if !strings.Contains(got, want) {
				t.Errorf("report %q does not mention %q", got, want)
			}
		}
	})
}

func TestHasMismatch(t *testing.T) {
	passing := []golden.SlideResult{{Name: "01.png", BaselineSize: image.Pt(1, 1), CandidateSize: image.Pt(1, 1)}}
	if golden.HasMismatch(passing, 0.01) {
		t.Error("want no mismatch among passing results")
	}

	failing := []golden.SlideResult{{Name: "01.png", BaselineSize: image.Pt(1, 1), CandidateSize: image.Pt(2, 2)}}
	if !golden.HasMismatch(failing, 1.0) {
		t.Error("want a dimension mismatch to be reported regardless of tolerance")
	}
}
