package cli

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

func writeSolidPNG(t *testing.T, dir, name string, w, h int, c color.Color) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
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

func TestGoldenRejectsBadFlags(t *testing.T) {
	if code, _, _ := exec("golden", "-nonsense"); code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
}

func TestGoldenRequiresDir(t *testing.T) {
	code, _, stderr := exec("golden")
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "-dir is required") {
		t.Errorf("stderr does not mention the missing flag:\n%s", stderr)
	}
}

func TestGoldenOptionDefaults(t *testing.T) {
	opts, err := parseGoldenOptions([]string{"-dir", "x"}, os.Stderr)
	if err != nil {
		t.Fatalf("parseGoldenOptions: %v", err)
	}
	if opts.baseline != defaultGoldenBaseline {
		t.Errorf("baseline = %q, want %q", opts.baseline, defaultGoldenBaseline)
	}
	if opts.diffDir != defaultGoldenDiffDir {
		t.Errorf("diffDir = %q, want %q", opts.diffDir, defaultGoldenDiffDir)
	}
	if opts.tolerance != golden.DefaultDiffRatioTolerance {
		t.Errorf("tolerance = %v, want %v", opts.tolerance, golden.DefaultDiffRatioTolerance)
	}
}

func TestGoldenIsReachableFromDispatch(t *testing.T) {
	code, _, _ := exec("golden", "-dir", t.TempDir())
	if code == exitUsage {
		t.Error("golden is not wired into dispatch")
	}
}

func TestGoldenReportsAnUnreadableBaseline(t *testing.T) {
	code, _, stderr := exec("golden", "-dir", t.TempDir(), "-baseline", filepath.Join(t.TempDir(), "absent"))
	if code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr, "golden:") {
		t.Errorf("stderr does not report the failure:\n%s", stderr)
	}
}

func TestGoldenPassesOnAMatchingBaseline(t *testing.T) {
	baseline, candidate := t.TempDir(), t.TempDir()
	writeSolidPNG(t, baseline, "01.png", 10, 10, color.White)
	writeSolidPNG(t, candidate, "01.png", 10, 10, color.White)

	code, stdout, stderr := exec("golden", "-dir", candidate, "-baseline", baseline, "-diff-dir", t.TempDir())
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, "slide 1: ok") {
		t.Errorf("stdout does not report the passing slide:\n%s", stdout)
	}
}

func TestGoldenFailsAndNamesTheSlideOnAMismatch(t *testing.T) {
	baseline, candidate := t.TempDir(), t.TempDir()
	writeSolidPNG(t, baseline, "01.png", 10, 10, color.White)
	writeSolidPNG(t, candidate, "01.png", 10, 10, color.Black)

	diffDir := filepath.Join(t.TempDir(), "diffs")
	code, stdout, _ := exec("golden", "-dir", candidate, "-baseline", baseline, "-diff-dir", diffDir)
	if code != exitError {
		t.Fatalf("exit code = %d, want %d\nstdout: %s", code, exitError, stdout)
	}
	if !strings.Contains(stdout, "slide 1") || !strings.Contains(stdout, "FAIL") {
		t.Errorf("stdout does not name the failing slide:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(diffDir, "01.diff.png")); err != nil {
		t.Errorf("diff artifact was not written: %v", err)
	}
}

// TestGoldenNeverWritesToTheBaseline is the point of there being no -update
// flag: nothing runGolden can be asked to do should touch -baseline at all.
func TestGoldenNeverWritesToTheBaseline(t *testing.T) {
	baseline, candidate := t.TempDir(), t.TempDir()
	writeSolidPNG(t, baseline, "01.png", 10, 10, color.White)
	writeSolidPNG(t, candidate, "01.png", 10, 10, color.Black)

	before, err := os.ReadFile(filepath.Join(baseline, "01.png"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	exec("golden", "-dir", candidate, "-baseline", baseline, "-diff-dir", t.TempDir())

	after, err := os.ReadFile(filepath.Join(baseline, "01.png"))
	if err != nil {
		t.Fatalf("read fixture after run: %v", err)
	}
	if string(before) != string(after) {
		t.Error("the baseline file changed after a compare run")
	}
}
