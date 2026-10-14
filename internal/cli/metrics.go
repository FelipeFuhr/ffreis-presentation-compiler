package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine/chrome"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/metrics"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/render"
)

type metricsOptions struct {
	dir string
	out string
}

func parseMetricsOptions(args []string, stderr io.Writer) (*metricsOptions, error) {
	var o metricsOptions
	fs := flag.NewFlagSet("metrics", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.dir, "dir", "dist", "directory holding a rendered deck (index.html)")
	fs.StringVar(&o.out, "out", "", "file to write the metrics JSON to (default: stdout)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return &o, nil
}

// newMetricsBrowser is a variable for the same reason engine.LookPath and
// rendererFor are: it is the one line in this file that needs a real browser,
// and stubbing it lets a test drive runMetrics end to end — the JSON encoding,
// the -out handling and the failure paths — without Chrome deciding whether
// the test passes.
var newMetricsBrowser = func() metrics.Browser { return chrome.New() }

// runMetrics extracts per-slide layout metrics from a rendered deck and
// writes them as JSON. This has standalone value beyond `make review`: it is
// the input to any review, human or model.
func runMetrics(args []string, stdout, stderr io.Writer) int {
	opts, err := parseMetricsOptions(args, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "metrics: %v\n", err)
		return exitUsage
	}

	index, err := filepath.Abs(filepath.Join(opts.dir, render.IndexFile))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "metrics: %v\n", err)
		return exitError
	}

	results, err := metrics.Run(context.Background(), newMetricsBrowser(), engine.FileURL(index))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "metrics: %v\n", err)
		return exitError
	}

	encoded, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "metrics: %v\n", err)
		return exitError
	}
	return writeMetrics(opts.out, encoded, stdout, stderr)
}

// writeMetrics prints encoded to stdout, or writes it to out when given,
// printing the path written so a caller (or a human) can find it.
func writeMetrics(out string, encoded []byte, stdout, stderr io.Writer) int {
	if out == "" {
		_, _ = fmt.Fprintln(stdout, string(encoded))
		return exitOK
	}
	if err := os.WriteFile(out, encoded, 0o600); err != nil {
		_, _ = fmt.Fprintf(stderr, "metrics: %v\n", err)
		return exitError
	}
	_, _ = fmt.Fprintln(stdout, out)
	return exitOK
}
