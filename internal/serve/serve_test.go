package serve_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/serve"
)

const (
	sampleDeck   = "../../examples/sample-deck"
	sampleThemes = "../../examples/themes"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestRunServesThenShutsDownOnContextCancellation drives the package through
// its public API only: New, then Run on an OS-assigned loopback port
// ("127.0.0.1:0" — never a fixed one, so this cannot collide with anything
// else on the machine or in a parallel test run). It confirms the server
// actually answers a request carrying the live-reload script, and that it
// stops cleanly once its context is canceled — the same sequence SIGINT
// drives from the CLI.
func TestRunServesThenShutsDownOnContextCancellation(t *testing.T) {
	srv, err := serve.New(serve.Options{
		DeckRoot:  sampleDeck,
		ThemeRoot: sampleThemes,
		Addr:      "127.0.0.1:0",
		Lang:      "en",
	}, discardLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	defer cancel()

	addr := waitForAddr(t, srv)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s/", addr), nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "/__reload") {
		t.Fatalf("served page does not carry the reload script:\n%s", body)
	}

	cancel()

	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run returned an error after shutdown: %v", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context was canceled")
	}
}

func TestNewRejectsAnEmptyDeckOrThemeRoot(t *testing.T) {
	cases := []struct {
		name string
		opts serve.Options
	}{
		{"no deck root", serve.Options{ThemeRoot: sampleThemes}},
		{"no theme root", serve.Options{DeckRoot: sampleDeck}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := serve.New(tc.opts, discardLogger()); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

// TestNewSurfacesATempDirCreationFailure forces os.MkdirTemp to fail by
// pointing TMPDIR at a path that does not exist, rather than guessing at an
// unwritable one — portable and leaves nothing to clean up.
func TestNewSurfacesATempDirCreationFailure(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "does-not-exist"))

	_, err := serve.New(serve.Options{DeckRoot: sampleDeck, ThemeRoot: sampleThemes}, discardLogger())
	if err == nil {
		t.Fatal("want an error when the temp preview directory cannot be created")
	}
}

// TestRunSurfacesAListenFailureBeforeAnyRequestCanBeServed uses a
// syntactically invalid address so net.Listen fails immediately — no port is
// ever opened, fixed or otherwise, so this is fast and cannot collide with
// anything else on the machine.
func TestRunSurfacesAListenFailureBeforeAnyRequestCanBeServed(t *testing.T) {
	srv, err := serve.New(serve.Options{DeckRoot: sampleDeck, ThemeRoot: sampleThemes, Addr: "not-an-address"}, discardLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = srv.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "listen on") {
		t.Fatalf("Run error = %v, want a listen failure", err)
	}
}

// waitForAddr polls Addr() until Run has started listening. Run performs a
// synchronous first render before it binds, so this also covers the startup
// path taking a moment on a loaded machine.
func waitForAddr(t *testing.T, srv *serve.Server) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if addr := srv.Addr(); addr != "" {
			return addr
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("server never reported a bound address")
	return ""
}
