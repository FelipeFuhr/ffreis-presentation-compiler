// Package logx builds the one logger the CLI uses. Diagnostics go to stderr so
// stdout stays clean for artifact paths a caller may want to pipe.
package logx

import (
	"io"
	"log/slog"
)

// New returns a text logger at info level, or debug when verbose.
func New(w io.Writer, verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}
