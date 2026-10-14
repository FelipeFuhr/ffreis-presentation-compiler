package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/logx"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/serve"
)

// defaultServeAddr binds to loopback only. A deck is unpublished client
// material, and the default must not expose it on the LAN — ":8080" (all
// interfaces) would. An author who wants LAN access can pass
// "-addr 0.0.0.0:8080" explicitly.
const defaultServeAddr = "127.0.0.1:8080"

type serveOptions struct {
	deckRoot  string
	themeRoot string
	addr      string
	lang      string
}

func parseServeOptions(args []string, stderr io.Writer) (*serveOptions, error) {
	var o serveOptions
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.deckRoot, "deck-root", "", "deck directory (deck.yaml + slides/)")
	fs.StringVar(&o.themeRoot, "theme-root", "", "directory holding themes, one subdirectory per theme")
	fs.StringVar(&o.addr, "addr", defaultServeAddr, "address to listen on (loopback by default)")
	fs.StringVar(&o.lang, "lang", "pt-BR", "document language")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if o.deckRoot == "" {
		return nil, fmt.Errorf("-deck-root is required")
	}
	if o.themeRoot == "" {
		return nil, fmt.Errorf("-theme-root is required")
	}
	return &o, nil
}

// runServe renders the deck once, serves it on a loopback address, and
// re-renders plus reloads the browser whenever a deck or theme source file
// changes, until interrupted.
func runServe(args []string, stdout, stderr io.Writer) int {
	opts, err := parseServeOptions(args, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "serve: %v\n", err)
		return exitUsage
	}

	srv, err := serve.New(serve.Options{
		DeckRoot:  opts.deckRoot,
		ThemeRoot: opts.themeRoot,
		Addr:      opts.addr,
		Lang:      opts.lang,
	}, logx.New(stderr, false))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "serve: %v\n", err)
		return exitError
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	_, _ = fmt.Fprintf(stdout, "serving on http://%s (ctrl-c to stop)\n", opts.addr)
	if err := srv.Run(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "serve: %v\n", err)
		return exitError
	}
	return exitOK
}
