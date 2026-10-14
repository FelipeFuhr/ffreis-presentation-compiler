// Package engine is the ports-and-adapters layer over the external render
// toolchain. HTML is produced in-process by internal/render; every format
// derived from it (PDF today, PNG and PPTX next) sits behind a Renderer, and
// adapters shell out through an injectable Runner so command construction is
// unit-tested without a browser installed.
//
// This package is deliberately slide-agnostic: it knows about pages, sizes and
// files, never about decks, themes or layouts. That is what lets it become the
// fleet's shared html-render capability.
package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
)

// Format identifies an output target derived from the rendered HTML.
type Format string

const (
	FormatPDF Format = "pdf"
	FormatPNG Format = "png"
)

// Job describes one render request.
type Job struct {
	// IndexPath is the absolute path to the rendered index.html.
	IndexPath string
	// OutDir is where artifacts are written.
	OutDir string
	// Slug names the artifacts (<slug>.pdf).
	Slug string
	// Size is the page size; the PDF path sets it explicitly as well as via
	// CSS, because a theme could otherwise silently resize every page.
	Size deck.Size
}

// Renderer turns a Job into an artifact of a single Format.
type Renderer interface {
	Format() Format
	Tool() string
	Available() bool
	Render(context.Context, Job) (string, error)
}

// Runner executes an external command. It is injected so adapters can be
// unit-tested without the real toolchain installed.
type Runner func(ctx context.Context, name string, args ...string) error

// ExecRunner is the production Runner; tool chatter goes to stderr so stdout
// stays machine-readable.
func ExecRunner(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // invoking the configured browser binary is the purpose of this adapter
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// LookPath reports whether name resolves on PATH. Exposed as a variable so
// tests can simulate a missing toolchain.
var LookPath = func(name string) (string, error) { return exec.LookPath(name) }

func toolAvailable(name string) bool {
	_, err := LookPath(name)
	return err == nil
}

// ParseFormats turns a comma-separated flag value into Formats, rejecting
// unknown names rather than silently skipping them.
func ParseFormats(csv string) ([]Format, error) {
	if csv == "" {
		return nil, nil
	}
	known := map[string]Format{string(FormatPDF): FormatPDF, string(FormatPNG): FormatPNG}
	var out []Format
	for _, field := range strings.Split(csv, ",") {
		name := strings.TrimSpace(field)
		switch name {
		case "":
			continue
		case "html":
			// HTML is always produced; accepting it keeps `-formats html,pdf`
			// from being a confusing error.
			continue
		}
		f, ok := known[name]
		if !ok {
			return nil, fmt.Errorf("unknown format %q: want html, pdf or png", name)
		}
		out = append(out, f)
	}
	return out, nil
}
