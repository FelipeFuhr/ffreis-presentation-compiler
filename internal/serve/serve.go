package serve

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// shutdownTimeout bounds how long Run waits for in-flight requests to drain
// on SIGINT before giving up. A var, not a const, so a test can shorten it to
// force a deterministic Shutdown deadline instead of waiting out a real one.
var shutdownTimeout = 5 * time.Second

// readHeaderTimeout bounds how long a client may take to send request
// headers. This is a local preview server, not internet-facing, but setting
// it costs nothing and is what gosec's G112 (Slowloris) rule expects.
const readHeaderTimeout = 10 * time.Second

// Server owns one serve run: the render state, the change watcher and the
// HTTP server. Construct with New, then call Run.
type Server struct {
	opts   Options
	logger *slog.Logger
	outDir string
	site   *site
	broker *broker
	theme  themeState

	addrMu sync.Mutex
	addr   string
}

// New prepares a Server. It creates the temporary directory the deck is
// rendered into but does not render or listen yet — that happens in Run.
//
// DeckRoot and ThemeRoot are validated here rather than left to the first
// render: internal/serve is usable without the CLI (which validates them
// itself), and an empty root would otherwise resolve against the process's
// working directory inside deck.Load/theme.Load — a confusing failure mode
// for a direct caller of this package.
func New(opts Options, logger *slog.Logger) (*Server, error) {
	if opts.DeckRoot == "" {
		return nil, fmt.Errorf("deck root is required")
	}
	if opts.ThemeRoot == "" {
		return nil, fmt.Errorf("theme root is required")
	}
	outDir, err := os.MkdirTemp("", "ffreis-presentation-compiler-serve-*")
	if err != nil {
		return nil, fmt.Errorf("create preview directory: %w", err)
	}
	return &Server{
		opts:   opts,
		logger: logger,
		outDir: outDir,
		site:   newSite(),
		broker: newBroker(),
	}, nil
}

// Run renders once, starts serving on opts.Addr, and watches for changes
// until ctx is done, then shuts the HTTP server down gracefully. The
// temporary preview directory is removed on return.
func (s *Server) Run(ctx context.Context) error {
	defer func() { _ = os.RemoveAll(s.outDir) }() // best-effort: temp preview output, not durable state

	s.rebuild() // seed a first render so there is something to serve immediately

	var listenConfig net.ListenConfig
	ln, err := listenConfig.Listen(ctx, "tcp", s.opts.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.opts.Addr, err)
	}
	s.setAddr(ln.Addr().String())
	httpServer := &http.Server{Handler: s.routes(), ReadHeaderTimeout: readHeaderTimeout}
	s.logger.Info("serving", "addr", s.Addr(), "deck", s.opts.DeckRoot)

	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	go s.watchLoop(watchCtx)

	serveErr := make(chan error, 1)
	go func() { serveErr <- runServer(httpServer, ln) }()

	select {
	case <-ctx.Done():
		return s.shutdown(httpServer, serveErr)
	case err := <-serveErr:
		return err
	}
}

// Addr returns the actual bound address once Run has started listening, or
// "" beforehand. Useful when Options.Addr requests an OS-assigned port
// ("127.0.0.1:0") and a caller needs to know which one was chosen.
func (s *Server) Addr() string {
	s.addrMu.Lock()
	defer s.addrMu.Unlock()
	return s.addr
}

func (s *Server) setAddr(addr string) {
	s.addrMu.Lock()
	s.addr = addr
	s.addrMu.Unlock()
}

// runServer serves until the listener closes, treating the expected
// post-Shutdown error as success rather than a failure.
func runServer(httpServer *http.Server, ln net.Listener) error {
	if err := httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// shutdown drains in-flight requests within shutdownTimeout, then waits for
// the serve goroutine to confirm the listener is closed.
func (s *Server) shutdown(httpServer *http.Server, serveErr <-chan error) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down server: %w", err)
	}
	return <-serveErr
}
