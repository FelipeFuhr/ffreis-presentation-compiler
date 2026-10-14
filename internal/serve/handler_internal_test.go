package serve

// Server's handler composes unexported state (site, broker, outDir), so this
// file lives in package serve rather than serve_test. It still drives the
// handler the way a real client would, via httptest — no real listener.

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	sampleDeck   = "../../examples/sample-deck"
	sampleThemes = "../../examples/themes"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestServer(t *testing.T, opts Options) *Server {
	t.Helper()
	outDir := t.TempDir()
	return &Server{
		opts:   opts,
		logger: discardLogger(),
		outDir: outDir,
		site:   newSite(),
		broker: newBroker(),
	}
}

// TestRebuildInjectsTheScriptIntoTheServedPageButNotTheDiskFile locks in the
// requirement that live-reload is a serve-time concern only: the HTML
// render.Render wrote to disk must stay byte-for-byte what `build` would
// have produced, while what a browser receives carries the reload script.
func TestRebuildInjectsTheScriptIntoTheServedPageButNotTheDiskFile(t *testing.T) {
	s := newTestServer(t, Options{DeckRoot: sampleDeck, ThemeRoot: sampleThemes, Lang: "en"})

	s.rebuild()

	onDisk, err := os.ReadFile(filepath.Join(s.outDir, "index.html"))
	if err != nil {
		t.Fatalf("read rendered index.html: %v", err)
	}
	if strings.Contains(string(onDisk), "/__reload") {
		t.Fatalf("the built artifact on disk was modified with the reload script:\n%s", onDisk)
	}

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/", nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "/__reload") {
		t.Fatalf("served body does not carry the reload script:\n%s", rec.Body.String())
	}
}

func TestServeIndexAlsoAnswersSlashIndexHTML(t *testing.T) {
	s := newTestServer(t, Options{})
	s.site.setGood([]byte("<html><body>hi</body></html>"))

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/index.html", nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), "hi") {
		t.Fatalf("GET /index.html did not serve the current page:\n%s", rec.Body.String())
	}
}

func TestOtherPathsFallBackToTheRenderedFiles(t *testing.T) {
	s := newTestServer(t, Options{})
	if err := os.MkdirAll(filepath.Join(s.outDir, "assets"), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.outDir, "assets", "logo.svg"), []byte("<svg/>"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/assets/logo.svg", nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)

	if rec.Code != 200 || rec.Body.String() != "<svg/>" {
		t.Fatalf("status=%d body=%q, want 200 and the file's content", rec.Code, rec.Body.String())
	}
}

// TestFailedRebuildKeepsServingTheLastGoodPageWithTheErrorVisible is the
// error-path requirement: a render failure must not take the preview down,
// and the author must be able to see what broke.
func TestFailedRebuildKeepsServingTheLastGoodPageWithTheErrorVisible(t *testing.T) {
	s := newTestServer(t, Options{})
	s.site.setGood([]byte("<html><body>GOOD CONTENT MARKER</body></html>"))
	s.site.setErr(`parse slides/02-headline.md: front-matter fence "---" is never closed`)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/", nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "GOOD CONTENT MARKER") {
		t.Fatalf("last good render was lost after a failed rebuild:\n%s", body)
	}
	if !strings.Contains(body, "front-matter fence") {
		t.Fatalf("the rebuild error is not visible on the page:\n%s", body)
	}
}

func TestFailedFirstRenderServesAStandaloneErrorPage(t *testing.T) {
	s := newTestServer(t, Options{})
	s.site.setErr("deck.yaml: no such file or directory")

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/", nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), "deck.yaml: no such file or directory") {
		t.Fatalf("error from the first render is not visible:\n%s", rec.Body.String())
	}
}

func TestReloadEndpointServesServerSentEvents(t *testing.T) {
	s := newTestServer(t, Options{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the client has already gone; the handler must return, not hang
	req := httptest.NewRequestWithContext(ctx, "GET", "/__reload", nil)
	rec := httptest.NewRecorder()

	s.routes().ServeHTTP(rec, req)

	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
}
