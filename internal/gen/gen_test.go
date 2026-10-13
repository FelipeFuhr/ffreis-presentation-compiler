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

	path, err := gen.DefaultRegistry().Resolve(context.Background(), writingRunner(t, &cmd, "<svg/>"), req)
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
		return writingRunner(t, &cmd, "<svg/>")(ctx, name, args...)
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
		{"tool not installed", gen.Spec{Generator: "python", Spec: "chart.py"}, nil, false, "needs \"python3\""},
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
	if got != "cmd,mermaid,python" {
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
	err := gen.DefaultRegistry().Apply(context.Background(), writingRunner(t, &cmd, "<svg/>"), d,
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
			err := gen.DefaultRegistry().Apply(context.Background(), writingRunner(t, &cmd, "<svg/>"), d,
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
	if err := gen.DefaultRegistry().Apply(context.Background(), writingRunner(t, &cmd, "<svg/>"), d,
		gen.Options{DeckRoot: root, OutDir: t.TempDir()}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, ok := d.Slides[0].Fields[gen.MediaKey]; !ok {
		t.Error("a diagram spec did not produce media")
	}
}
