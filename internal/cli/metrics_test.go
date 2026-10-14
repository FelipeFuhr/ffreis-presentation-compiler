package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMetricsRejectsBadFlags(t *testing.T) {
	if code, _, _ := exec("metrics", "-nonsense"); code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
}

func TestMetricsOptionDefaults(t *testing.T) {
	opts, err := parseMetricsOptions(nil, io.Discard)
	if err != nil {
		t.Fatalf("parseMetricsOptions: %v", err)
	}
	if opts.dir != "dist" {
		t.Errorf("dir = %q, want dist", opts.dir)
	}
	if opts.out != "" {
		t.Errorf("out = %q, want empty (stdout by default)", opts.out)
	}
}

func TestMetricsIsReachableFromDispatch(t *testing.T) {
	// Dispatch must route "metrics" somewhere; with no rendered deck at -dir
	// this is enough to prove the verb is wired, without needing Chrome to
	// succeed — only to not be routed to "unknown command".
	code, _, _ := exec("metrics", "-dir", t.TempDir())
	if code == exitUsage {
		t.Error("metrics is not wired into dispatch")
	}
}

// TestWriteMetricsWritesToAFileWhenGivenOne exercises the split between
// stdout and -out directly, without needing a browser: it is the one branch
// in runMetrics that does not touch Chrome.
func TestWriteMetricsWritesToAFileWhenGivenOne(t *testing.T) {
	var stdout, stderr strings.Builder
	path := filepath.Join(t.TempDir(), "metrics.json")

	code := writeMetrics(path, []byte(`[{"slide":1}]`), &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", code, exitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), path) {
		t.Errorf("stdout does not name the written path:\n%s", stdout.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != `[{"slide":1}]` {
		t.Errorf("file content = %q", got)
	}
}

func TestWriteMetricsPrintsToStdoutByDefault(t *testing.T) {
	var stdout, stderr strings.Builder
	code := writeMetrics("", []byte(`[{"slide":1}]`), &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", code, exitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"slide":1`) {
		t.Errorf("stdout does not carry the JSON:\n%s", stdout.String())
	}
}

func TestWriteMetricsReportsAnUnwritablePath(t *testing.T) {
	var stdout, stderr strings.Builder
	code := writeMetrics(filepath.Join(t.TempDir(), "absent-dir", "metrics.json"), []byte("[]"), &stdout, &stderr)
	if code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr.String(), "metrics:") {
		t.Errorf("stderr does not report the failure:\n%s", stderr.String())
	}
}
