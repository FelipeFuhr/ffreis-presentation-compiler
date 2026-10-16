package gen_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/gen"
)

// validSVG stands in for a real generator's output: large enough to clear
// gen.MinArtifactBytes and shaped like an actual <svg>…</svg> document, so
// tests exercise the same artifact-validation path real output does instead
// of being a special case too small or too malformed to reach it.
var validSVG = "<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 100 100\">" +
	strings.Repeat("<!-- padding to clear the minimum artifact size --> ", 10) +
	"<rect width=\"100\" height=\"100\" fill=\"#02accf\"/></svg>"

// stubLookPath makes generator availability deterministic.
func stubLookPath(t *testing.T, available bool) {
	t.Helper()
	original := gen.LookPath
	gen.LookPath = func(name string) (string, error) {
		if available {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() { gen.LookPath = original })
}

// deckWithSpec writes a spec file (and optional data) and returns the root.
func deckWithSpec(t *testing.T, data string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "chart.py"), []byte("# spec"), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	if data != "" {
		if err := os.WriteFile(filepath.Join(root, "data.csv"), []byte(data), 0o600); err != nil {
			t.Fatalf("write data: %v", err)
		}
	}
	return root
}

// writingRunner stands in for a generator: it records the command and writes
// the output file the real tool would have produced.
func writingRunner(t *testing.T, captured *[]string, content string) gen.Runner {
	t.Helper()
	return func(_ context.Context, name string, args ...string) error {
		*captured = append([]string{name}, args...)
		for i, a := range args {
			if a == "--out" && i+1 < len(args) {
				return os.WriteFile(args[i+1], []byte(content), 0o600)
			}
		}
		return errors.New("runner: no --out in args")
	}
}

func TestResolveRunsTheGeneratorAndReturnsARelativePath(t *testing.T) {
	stubLookPath(t, true)
	root := deckWithSpec(t, "a,1\n")
	out := t.TempDir()

	var cmd []string
	req := gen.Request{
		Spec:     gen.Spec{Generator: "python", Spec: "chart.py", Data: "data.csv", Args: []string{"--title", "T"}},
		DeckRoot: root,
		OutDir:   out,
		Tokens:   map[string]string{"accent": "#02accf"},
	}

	path, err := gen.DefaultRegistry().Resolve(context.Background(), writingRunner(t, &cmd, validSVG), req)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if !strings.HasPrefix(path, gen.Dir+"/") || !strings.HasSuffix(path, ".svg") {
		t.Errorf("Resolve returned %q, want a %s/<hash>.svg path", path, gen.Dir)
	}
	if _, err := os.Stat(filepath.Join(out, path)); err != nil {
		t.Errorf("artifact was not written: %v", err)
	}

	joined := strings.Join(cmd, " ")
	// The token payload is the whole point of the hook: a generator that is
	// not handed the theme cannot match it.
	for _, want := range []string{"python3", "chart.py", "--tokens", "#02accf", "--data", "--title"} {
		if !strings.Contains(joined, want) {
			t.Errorf("command %q is missing %q", joined, want)
		}
	}
}

func TestResolveCachesOnContent(t *testing.T) {
	stubLookPath(t, true)
	root := deckWithSpec(t, "a,1\n")
	out := t.TempDir()
	req := gen.Request{
		Spec:     gen.Spec{Generator: "python", Spec: "chart.py", Data: "data.csv"},
		DeckRoot: root,
		OutDir:   out,
		Tokens:   map[string]string{"accent": "#02accf"},
	}

	runs := 0
	counting := func(ctx context.Context, name string, args ...string) error {
		runs++
		var cmd []string
		return writingRunner(t, &cmd, validSVG)(ctx, name, args...)
	}

	reg := gen.DefaultRegistry()
	first, err := reg.Resolve(context.Background(), counting, req)
	if err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	second, err := reg.Resolve(context.Background(), counting, req)
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if first != second || runs != 1 {
		t.Errorf("identical input ran the generator %d times (paths %q, %q); want 1", runs, first, second)
	}

	// Changing the tokens alone must invalidate: the chart's colours came from
	// them, so a cached image would now be wrong.
	req.Tokens = map[string]string{"accent": "#ff0000"}
	third, err := reg.Resolve(context.Background(), counting, req)
	if err != nil {
		t.Fatalf("third Resolve: %v", err)
	}
	if third == first || runs != 2 {
		t.Errorf("changing tokens did not regenerate (runs=%d, path %q)", runs, third)
	}
}

func TestResolveFailureModes(t *testing.T) {
	root := deckWithSpec(t, "")
	var cmd []string

	cases := []struct {
		name    string
		spec    gen.Spec
		runner  gen.Runner
		tool    bool
		wantErr string
	}{
		{"unknown generator", gen.Spec{Generator: "nope", Spec: "chart.py"}, nil, true, "unknown generator"},
		{"no spec file named", gen.Spec{Generator: "python"}, nil, true, "no spec file"},
		{"missing spec file", gen.Spec{Generator: "python", Spec: "absent.py"}, nil, true, "no such file"},
		{"missing data file", gen.Spec{Generator: "python", Spec: "chart.py", Data: "absent.csv"}, nil, true, "no such file"},
		{
			"spec path escapes the deck root",
			gen.Spec{Generator: "python", Spec: "../../etc/passwd"},
			nil, true, "escapes",
		},
		{
			"data path escapes the deck root",
			gen.Spec{Generator: "python", Spec: "chart.py", Data: "../../etc/passwd"},
			nil, true, "escapes",
		},
		{"tool not installed", gen.Spec{Generator: "python", Spec: "chart.py"}, nil, false, "needs \"python3\""},
		{"d2 not installed", gen.Spec{Generator: "d2", Spec: "chart.py"}, nil, false, "needs \"d2\""},
		{
			"generator exits non-zero",
			gen.Spec{Generator: "python", Spec: "chart.py"},
			func(context.Context, string, ...string) error { return errors.New("boom") },
			true, "boom",
		},
		{
			"generator writes nothing",
			gen.Spec{Generator: "python", Spec: "chart.py"},
			func(context.Context, string, ...string) error { return nil },
			true, "wrote no output",
		},
		{
			"generator writes an empty file",
			gen.Spec{Generator: "python", Spec: "chart.py"},
			writingRunner(t, &cmd, ""),
			true, "wrote no output",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubLookPath(t, tc.tool)
			req := gen.Request{Spec: tc.spec, DeckRoot: root, OutDir: t.TempDir()}
			_, err := gen.DefaultRegistry().Resolve(context.Background(), tc.runner, req)
			if err == nil {
				t.Fatalf("want an error mentioning %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestRegistryNames(t *testing.T) {
	got := strings.Join(gen.DefaultRegistry().Names(), ",")
	if got != "cmd,d2,mermaid,python" {
		t.Errorf("Names() = %q", got)
	}
}

func TestApplySetsMediaFromAChartSpec(t *testing.T) {
	stubLookPath(t, true)
	root := deckWithSpec(t, "a,1\n")
	out := t.TempDir()

	d := &deck.Deck{Slides: []deck.Slide{
		{Number: 1, Layout: "split", Source: "01.md", Fields: map[string]any{
			"chart": map[string]any{"generator": "python", "spec": "chart.py", "data": "data.csv"},
		}},
		{Number: 2, Layout: "bullets", Source: "02.md", Fields: map[string]any{"items": []any{"x"}}},
	}}

	var cmd []string
	err := gen.DefaultRegistry().Apply(context.Background(), writingRunner(t, &cmd, validSVG), d,
		gen.Options{DeckRoot: root, OutDir: out})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	media, ok := d.Slides[0].Fields[gen.MediaKey].(string)
	if !ok || !strings.HasSuffix(media, ".svg") {
		t.Errorf("slide 1 media = %v, want a generated svg path", d.Slides[0].Fields[gen.MediaKey])
	}
	if _, touched := d.Slides[1].Fields[gen.MediaKey]; touched {
		t.Error("slide 2 has no generator and must be left alone")
	}
}

func TestApplyRejectsAmbiguousOrMalformedSpecs(t *testing.T) {
	stubLookPath(t, true)
	root := deckWithSpec(t, "")

	cases := []struct {
		name    string
		fields  map[string]any
		wantErr string
	}{
		{
			"media and chart both set",
			map[string]any{
				"media": "assets/a.svg",
				"chart": map[string]any{"generator": "python", "spec": "chart.py"},
			},
			"sets both",
		},
		{
			"spec is not a mapping",
			map[string]any{"chart": "charts/a.py"},
			"must be a mapping",
		},
		{
			"generator not named",
			map[string]any{"chart": map[string]any{"spec": "chart.py"}},
			"does not name a generator",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &deck.Deck{Slides: []deck.Slide{{Number: 1, Source: "01.md", Fields: tc.fields}}}
			var cmd []string
			err := gen.DefaultRegistry().Apply(context.Background(), writingRunner(t, &cmd, validSVG), d,
				gen.Options{DeckRoot: root, OutDir: t.TempDir()})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want an error mentioning %q, got %v", tc.wantErr, err)
			}
			if !strings.Contains(err.Error(), "01.md") {
				t.Errorf("error %q does not name the slide file", err)
			}
		})
	}
}

func TestDiagramKeyIsAnAliasForChart(t *testing.T) {
	stubLookPath(t, true)
	root := deckWithSpec(t, "")
	d := &deck.Deck{Slides: []deck.Slide{{Number: 1, Source: "01.md", Fields: map[string]any{
		"diagram": map[string]any{"generator": "cmd", "spec": "chart.py"},
	}}}}

	var cmd []string
	if err := gen.DefaultRegistry().Apply(context.Background(), writingRunner(t, &cmd, validSVG), d,
		gen.Options{DeckRoot: root, OutDir: t.TempDir()}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, ok := d.Slides[0].Fields[gen.MediaKey]; !ok {
		t.Error("a diagram spec did not produce media")
	}
}

func TestD2InjectsThemeTokensIntoTheSpecBeforeInvocation(t *testing.T) {
	stubLookPath(t, true)
	root := t.TempDir()
	specPath := filepath.Join(root, "diagram.d2")
	if err := os.WriteFile(specPath, []byte("a -> b\n"), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	out := t.TempDir()

	var seenSpecPath string
	var seenSpecContent []byte
	runner := func(_ context.Context, name string, args ...string) error {
		if name != "d2" {
			t.Errorf("command = %q, want d2", name)
		}
		if len(args) < 2 {
			t.Fatalf("args = %v, want at least an input and an output path", args)
		}
		seenSpecPath = args[0]
		b, err := os.ReadFile(seenSpecPath)
		if err != nil {
			t.Fatalf("read rewritten spec: %v", err)
		}
		seenSpecContent = b
		return os.WriteFile(args[1], []byte(validSVG), 0o600)
	}

	req := gen.Request{
		Spec:     gen.Spec{Generator: "d2", Spec: "diagram.d2"},
		DeckRoot: root,
		OutDir:   out,
		Tokens: map[string]string{
			"accent":      "#02accf",
			"accent-text": "#0e3c45",
			// Not hex: d2 rejects anything but a named colour or a hex code in
			// a theme-overrides slot, so this must be dropped rather than
			// forwarded and left to fail at the d2 compile step.
			"accent-soft": "rgba(2, 172, 207, 0.1)",
		},
	}

	if _, err := gen.DefaultRegistry().Resolve(context.Background(), runner, req); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if seenSpecPath == specPath {
		t.Fatal("d2 ran on the author's own spec file; token injection must rewrite a copy, not the original")
	}
	for _, want := range []string{`B1: "#02accf"`, `N1: "#0e3c45"`, "a -> b"} {
		if !strings.Contains(string(seenSpecContent), want) {
			t.Errorf("rewritten spec %q is missing %q", seenSpecContent, want)
		}
	}
	if strings.Contains(string(seenSpecContent), "rgba") {
		t.Error("rewritten spec forwarded a non-hex token value, which d2 rejects outright")
	}
	if _, err := os.Stat(seenSpecPath); !os.IsNotExist(err) {
		t.Errorf("temp spec %q was left behind after Resolve", seenSpecPath)
	}
}

func TestD2CommandIsPositionalNotFlagBased(t *testing.T) {
	stubLookPath(t, true)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "diagram.d2"), []byte("a -> b\n"), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	out := t.TempDir()

	var cmd []string
	runner := func(_ context.Context, name string, args ...string) error {
		cmd = append([]string{name}, args...)
		return os.WriteFile(args[1], []byte(validSVG), 0o600)
	}

	req := gen.Request{
		Spec:     gen.Spec{Generator: "d2", Spec: "diagram.d2", Args: []string{"--layout", "elk"}},
		DeckRoot: root,
		OutDir:   out,
	}
	if _, err := gen.DefaultRegistry().Resolve(context.Background(), runner, req); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if len(cmd) < 4 || cmd[0] != "d2" || cmd[len(cmd)-2] != "--layout" || cmd[len(cmd)-1] != "elk" {
		t.Errorf("command = %v, want [d2 <input> <output> --layout elk]", cmd)
	}
	if strings.Contains(strings.Join(cmd, " "), "--out") {
		t.Error("d2's command line must not carry --out: it takes a positional output path")
	}
}

func TestResolveRejectsAnArtifactUnderTheMinimumSize(t *testing.T) {
	stubLookPath(t, true)
	root := deckWithSpec(t, "")
	req := gen.Request{Spec: gen.Spec{Generator: "python", Spec: "chart.py"}, DeckRoot: root, OutDir: t.TempDir()}

	var cmd []string
	tooSmall := "<svg xmlns=\"http://www.w3.org/2000/svg\"></svg>" // well-formed, but under MinArtifactBytes
	_, err := gen.DefaultRegistry().Resolve(context.Background(), writingRunner(t, &cmd, tooSmall), req)
	if err == nil || !strings.Contains(err.Error(), "wrote only") {
		t.Fatalf("err = %v, want an error naming the artifact as too small", err)
	}
}

func TestResolveRejectsContentThatDoesNotLookLikeSVG(t *testing.T) {
	stubLookPath(t, true)
	root := deckWithSpec(t, "")
	req := gen.Request{Spec: gen.Spec{Generator: "python", Spec: "chart.py"}, DeckRoot: root, OutDir: t.TempDir()}

	// Large enough to clear MinArtifactBytes, but not SVG at all — the shape
	// of a crashed generator's stack trace or an unrelated file written to
	// the wrong path, not a diagram.
	notSVG := strings.Repeat("error: something went wrong while rendering the chart\n", 10)
	var cmd []string
	_, err := gen.DefaultRegistry().Resolve(context.Background(), writingRunner(t, &cmd, notSVG), req)
	if err == nil || !strings.Contains(err.Error(), "does not look like SVG") {
		t.Fatalf("err = %v, want an error naming the format mismatch", err)
	}
}

func TestResolveDoesNotCacheARejectedArtifact(t *testing.T) {
	stubLookPath(t, true)
	root := deckWithSpec(t, "")
	out := t.TempDir()
	req := gen.Request{Spec: gen.Spec{Generator: "python", Spec: "chart.py"}, DeckRoot: root, OutDir: out}

	runs := 0
	broken := func(ctx context.Context, name string, args ...string) error {
		runs++
		var cmd []string
		return writingRunner(t, &cmd, "too small")(ctx, name, args...)
	}

	reg := gen.DefaultRegistry()
	if _, err := reg.Resolve(context.Background(), broken, req); err == nil {
		t.Fatal("want the first Resolve to fail on an undersized artifact")
	}
	// A second attempt with the exact same inputs must run the generator
	// again, not silently "succeed" by reusing the broken file the first
	// attempt left at its content-hash path.
	if _, err := reg.Resolve(context.Background(), broken, req); err == nil {
		t.Fatal("want the second Resolve to fail too — nothing valid was ever produced")
	}
	if runs != 2 {
		t.Errorf("generator ran %d time(s), want 2 — a rejected artifact must not be cached", runs)
	}
}

func TestResolveRecoversOnceTheGeneratorIsFixed(t *testing.T) {
	stubLookPath(t, true)
	root := deckWithSpec(t, "")
	out := t.TempDir()
	req := gen.Request{Spec: gen.Spec{Generator: "python", Spec: "chart.py"}, DeckRoot: root, OutDir: out}

	attempt := 0
	flaky := func(ctx context.Context, name string, args ...string) error {
		attempt++
		var cmd []string
		if attempt == 1 {
			return writingRunner(t, &cmd, "too small")(ctx, name, args...)
		}
		return writingRunner(t, &cmd, validSVG)(ctx, name, args...)
	}

	reg := gen.DefaultRegistry()
	if _, err := reg.Resolve(context.Background(), flaky, req); err == nil {
		t.Fatal("want the first attempt to fail")
	}
	path, err := reg.Resolve(context.Background(), flaky, req)
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, path)); err != nil {
		t.Errorf("the fixed artifact was not written: %v", err)
	}
}
