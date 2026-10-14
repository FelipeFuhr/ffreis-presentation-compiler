// Package serve implements the live-reloading preview server: it renders a
// deck to a temporary directory, serves it over loopback HTTP, and polls the
// deck and theme sources for changes, re-rendering and nudging connected
// browsers to reload when something changes.
//
// It is kept independent of internal/cli so the render/watch/HTTP loop is
// unit-testable on its own; the CLI's `serve` subcommand is flags and wiring
// over this package's public API.
package serve

// Options controls one serve run. Like `build`, every path is a flag and the
// compiler hardcodes no consumer.
type Options struct {
	DeckRoot  string
	ThemeRoot string
	// Addr is the listen address. Defaults are the CLI's concern; this
	// package just binds whatever it is given.
	Addr string
	Lang string
}
