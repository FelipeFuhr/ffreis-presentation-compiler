package engine_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine/chrome"
)

// stubLookPath makes tool availability deterministic for the duration of a test.
func stubLookPath(t *testing.T, available bool) {
	t.Helper()
	original := engine.LookPath
	engine.LookPath = func(name string) (string, error) {
		if available {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() { engine.LookPath = original })
}

func TestParseFormats(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []engine.Format
	}{
		{"empty", "", nil},
		{"pdf only", "pdf", []engine.Format{engine.FormatPDF}},
		{"html is implicit and ignored", "html", nil},
		{"html alongside others", "html,pdf", []engine.Format{engine.FormatPDF}},
		{"whitespace and blanks tolerated", " pdf , ,png ", []engine.Format{engine.FormatPDF, engine.FormatPNG}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := engine.ParseFormats(tc.input)
			if err != nil {
				t.Fatalf("ParseFormats(%q): %v", tc.input, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("ParseFormats(%q) = %v, want %v", tc.input, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("ParseFormats(%q)[%d] = %q, want %q", tc.input, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestParseFormatsRejectsUnknownNames(t *testing.T) {
	// Silently skipping an unknown format would hand back a deck missing the
	// artifact the caller asked for.
	_, err := engine.ParseFormats("pdf,docx")
	if err == nil || !strings.Contains(err.Error(), "docx") {
		t.Fatalf("want an error naming the unknown format, got %v", err)
	}
}

func TestPDFRendererReportsAvailability(t *testing.T) {
	r := engine.NewPDFRenderer()

	stubLookPath(t, true)
	if !r.Available() {
		t.Error("Available() = false with a browser on PATH")
	}
	if r.Tool() != "google-chrome" {
		t.Errorf("Tool() = %q, want the first browser on PATH", r.Tool())
	}
	if r.Format() != engine.FormatPDF {
		t.Errorf("Format() = %q", r.Format())
	}

	stubLookPath(t, false)
	if r.Available() {
		t.Error("Available() = true with no browser on PATH")
	}
	if r.Tool() == "" {
		t.Error("Tool() must still name the preferred binary so doctor can suggest it")
	}
}

// newJob writes a stand-in index.html and returns a Job pointing at it.
func newJob(t *testing.T) engine.Job {
	t.Helper()
	dir := t.TempDir()
	index := filepath.Join(dir, "index.html")
	if err := os.WriteFile(index, []byte("<html></html>"), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}
	return engine.Job{
		IndexPath: index,
		OutDir:    dir,
		Slug:      "deck",
		Size:      deck.Size{WidthIn: 13.333, HeightIn: 7.5},
	}
}

func TestPDFRendererInvokesTheBrowserAndReturnsTheArtifact(t *testing.T) {
	stubLookPath(t, true)
	job := newJob(t)

	var gotName string
	var gotArgs []string
	r := &engine.PDFRenderer{Runner: func(_ context.Context, name string, args ...string) error {
		gotName, gotArgs = name, args
		// Stand in for the browser: produce the file it would have written.
		return os.WriteFile(filepath.Join(job.OutDir, "deck.pdf"), []byte("%PDF-1.4"), 0o600)
	}}

	path, err := r.Render(context.Background(), job)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if filepath.Base(path) != "deck.pdf" {
		t.Errorf("Render returned %q, want the slug-named pdf", path)
	}
	if gotName != "google-chrome" {
		t.Errorf("invoked %q", gotName)
	}

	args := strings.Join(gotArgs, " ")
	for _, want := range []string{"--headless=new", "--no-pdf-header-footer", "--no-sandbox", "--disable-dev-shm-usage", "file://" + job.IndexPath} {
		if !strings.Contains(args, want) {
			t.Errorf("browser args %q are missing %q", args, want)
		}
	}
}

func TestPDFRendererFailureModes(t *testing.T) {
	t.Run("no browser installed", func(t *testing.T) {
		stubLookPath(t, false)
		_, err := engine.NewPDFRenderer().Render(context.Background(), newJob(t))
		if err == nil || !strings.Contains(err.Error(), "no browser found") {
			t.Fatalf("want a \"no browser found\" error, got %v", err)
		}
	})

	t.Run("missing index.html", func(t *testing.T) {
		stubLookPath(t, true)
		job := newJob(t)
		job.IndexPath = filepath.Join(t.TempDir(), "absent.html")
		_, err := engine.NewPDFRenderer().Render(context.Background(), job)
		if err == nil {
			t.Fatal("want an error when the rendered HTML is missing")
		}
	})

	t.Run("browser exits non-zero", func(t *testing.T) {
		stubLookPath(t, true)
		r := &engine.PDFRenderer{Runner: func(context.Context, string, ...string) error {
			return errors.New("chrome blew up")
		}}
		_, err := r.Render(context.Background(), newJob(t))
		if err == nil || !strings.Contains(err.Error(), "chrome blew up") {
			t.Fatalf("want the browser error surfaced, got %v", err)
		}
	})

	t.Run("browser succeeds but writes nothing", func(t *testing.T) {
		// Chrome has been known to exit 0 having produced no file; treating
		// that as success would ship an empty deck.
		stubLookPath(t, true)
		r := &engine.PDFRenderer{Runner: func(context.Context, string, ...string) error { return nil }}
		_, err := r.Render(context.Background(), newJob(t))
		if err == nil || !strings.Contains(err.Error(), "wrote no file") {
			t.Fatalf("want a \"wrote no file\" error, got %v", err)
		}
	})

}

func TestExecRunnerSurfacesCommandFailure(t *testing.T) {
	err := engine.ExecRunner(context.Background(), "false")
	if err == nil {
		t.Fatal("want an error from a command that exits non-zero")
	}
	if err := engine.ExecRunner(context.Background(), "true"); err != nil {
		t.Errorf("want success from `true`, got %v", err)
	}
}

// fakeCapturer stands in for headless Chrome.
type fakeCapturer struct {
	shots    []chrome.Shot
	err      error
	selector string
	scale    float64
}

func (f *fakeCapturer) CaptureAll(_ context.Context, _, selector string, scale float64) ([]chrome.Shot, error) {
	f.selector, f.scale = selector, scale
	return f.shots, f.err
}

func twoShots() []chrome.Shot {
	return []chrome.Shot{{Index: 1, PNG: []byte("\x89PNG-one")}, {Index: 2, PNG: []byte("\x89PNG-two")}}
}

func TestPNGRendererWritesOneFilePerSlide(t *testing.T) {
	stubLookPath(t, true)
	job := newJob(t)
	cap := &fakeCapturer{shots: twoShots()}
	r := &engine.PNGRenderer{Client: cap, Scale: 3}

	dir, err := r.Render(context.Background(), job)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if filepath.Base(dir) != engine.SlidesDir {
		t.Errorf("Render returned %q, want the slides dir", dir)
	}
	for _, name := range []string{"01.png", "02.png"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s was not written: %v", name, err)
		}
	}
	if cap.selector != engine.SlideSelector {
		t.Errorf("captured %q, want %q", cap.selector, engine.SlideSelector)
	}
	if cap.scale != 3 {
		t.Errorf("scale = %v, want the configured 3", cap.scale)
	}
	if r.Format() != engine.FormatPNG {
		t.Errorf("Format() = %q", r.Format())
	}
}

func TestPNGRendererDefaultsTheScale(t *testing.T) {
	stubLookPath(t, true)
	cap := &fakeCapturer{shots: twoShots()}
	r := &engine.PNGRenderer{Client: cap}
	if _, err := r.Render(context.Background(), newJob(t)); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if cap.scale != engine.DefaultPNGScale {
		t.Errorf("scale = %v, want the default %v", cap.scale, engine.DefaultPNGScale)
	}
}

func TestPPTXRendererBuildsADeckWithNotes(t *testing.T) {
	stubLookPath(t, true)
	job := newJob(t)
	r := &engine.PPTXRenderer{
		Client: &fakeCapturer{shots: twoShots()},
		Notes:  map[int]string{1: "first", 2: "second"},
	}

	path, err := r.Render(context.Background(), job)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if filepath.Base(path) != "deck.pptx" {
		t.Errorf("Render returned %q, want the slug-named pptx", path)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		t.Fatalf("pptx was not written: %v", err)
	}
	if r.Format() != engine.FormatPPTX {
		t.Errorf("Format() = %q", r.Format())
	}
}

func TestExportAdapterFailureModes(t *testing.T) {
	t.Run("png without a browser", func(t *testing.T) {
		stubLookPath(t, false)
		_, err := engine.NewPNGRenderer().Render(context.Background(), newJob(t))
		if err == nil || !strings.Contains(err.Error(), "no browser found") {
			t.Fatalf("want a \"no browser found\" error, got %v", err)
		}
	})

	t.Run("pptx without a browser", func(t *testing.T) {
		stubLookPath(t, false)
		_, err := engine.NewPPTXRenderer().Render(context.Background(), newJob(t))
		if err == nil || !strings.Contains(err.Error(), "no browser found") {
			t.Fatalf("want a \"no browser found\" error, got %v", err)
		}
	})

	t.Run("capture fails", func(t *testing.T) {
		stubLookPath(t, true)
		r := &engine.PNGRenderer{Client: &fakeCapturer{err: errors.New("chrome exploded")}}
		if _, err := r.Render(context.Background(), newJob(t)); err == nil ||
			!strings.Contains(err.Error(), "chrome exploded") {
			t.Fatalf("want the capture error surfaced, got %v", err)
		}
	})

	t.Run("missing index.html", func(t *testing.T) {
		stubLookPath(t, true)
		job := newJob(t)
		job.IndexPath = filepath.Join(t.TempDir(), "absent.html")
		if _, err := (&engine.PPTXRenderer{Client: &fakeCapturer{shots: twoShots()}}).
			Render(context.Background(), job); err == nil {
			t.Fatal("want an error when the rendered HTML is missing")
		}
	})
}
