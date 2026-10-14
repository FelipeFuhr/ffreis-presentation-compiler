package serve

// runServer is unexported, so this file lives in package serve rather than
// serve_test.

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// listenOnLoopback opens a listener on an OS-assigned port, never a fixed
// one, so this cannot collide with anything else on the machine or in a
// parallel test run.
func listenOnLoopback(t *testing.T) net.Listener {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

func TestRunServerTreatsAGracefulShutdownAsSuccess(t *testing.T) {
	ln := listenOnLoopback(t)
	httpServer := &http.Server{Handler: http.NotFoundHandler()}

	errCh := make(chan error, 1)
	go func() { errCh <- runServer(httpServer, ln) }()

	// Give Serve a moment to start accepting before asking it to stop.
	time.Sleep(20 * time.Millisecond)
	if err := httpServer.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("runServer = %v, want nil after a graceful Shutdown", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runServer did not return after Shutdown")
	}
}

func TestRunServerSurfacesARealListenError(t *testing.T) {
	ln := listenOnLoopback(t)
	if err := ln.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	httpServer := &http.Server{Handler: http.NotFoundHandler()}

	// The listener is already closed, not Shutdown — Serve fails immediately
	// with something other than http.ErrServerClosed.
	if err := runServer(httpServer, ln); err == nil {
		t.Fatal("want an error when Serve is handed an already-closed listener")
	}
}

// TestShutdownWrapsAGenuineShutdownFailure forces http.Server.Shutdown
// itself to fail: shutdownTimeout is set to a near-zero value and an
// in-flight request is held open past it, so Shutdown returns
// context.DeadlineExceeded instead of draining cleanly.
func TestShutdownWrapsAGenuineShutdownFailure(t *testing.T) {
	origTimeout := shutdownTimeout
	shutdownTimeout = time.Nanosecond
	t.Cleanup(func() { shutdownTimeout = origTimeout })

	ln := listenOnLoopback(t)
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	httpServer := &http.Server{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		entered <- struct{}{}
		<-release // held open well past the near-zero shutdown deadline
	})}

	serveErr := make(chan error, 1)
	go func() { serveErr <- runServer(httpServer, ln) }()

	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		// This request's only job is to give Shutdown an in-flight
		// connection it cannot drain before the near-zero deadline.
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+ln.Addr().String()+"/", nil)
		if err != nil {
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-entered // the handler is now blocked inside, holding a live connection

	s := &Server{}
	err := s.shutdown(httpServer, serveErr)

	close(release)     // let the handler finish so nothing is left blocked
	httpServer.Close() // force-close now that the test's assertion is done
	<-clientDone       // best-effort: let the client goroutine exit before the test returns

	if err == nil || !strings.Contains(err.Error(), "shut down server") {
		t.Fatalf("shutdown error = %v, want a wrapped Shutdown failure", err)
	}
}
