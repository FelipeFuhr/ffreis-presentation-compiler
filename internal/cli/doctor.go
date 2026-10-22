package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/gen"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/parity"
)

// runDoctor reports adapter availability so a missing browser is diagnosed
// before a build fails halfway through.
func runDoctor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	renderers := []engine.Renderer{engine.NewPDFRenderer(), engine.NewPNGRenderer(), engine.NewPPTXRenderer()}

	allReady := true
	_, _ = fmt.Fprintln(stdout, "format  tool                  status")
	for _, r := range renderers {
		status := "ok"
		if !r.Available() {
			status = "MISSING"
			allReady = false
		}
		_, _ = fmt.Fprintf(stdout, "%-7s %-21s %s\n", r.Format(), r.Tool(), status)
	}

	_, _ = fmt.Fprintln(stdout, "\ngenerator tool                  status")
	registry := gen.DefaultRegistry()
	for _, name := range registry.Names() {
		status := "ok"
		if !registry[name].Available() {
			status = "optional, not installed"
		}
		_, _ = fmt.Fprintf(stdout, "%-9s %-21s %s\n", name, registry[name].Tool(), status)
	}

	_, _ = fmt.Fprintln(stdout, "\ncheck     tool                  status")
	parityStatus := "ok"
	if !parity.Available() {
		parityStatus = "optional, not installed"
	}
	_, _ = fmt.Fprintf(stdout, "%-9s %-21s %s\n", "parity", parity.Tool, parityStatus)

	// ooxml-validate is Microsoft's own Open XML SDK behind a small .NET
	// console app. Optional, like parity's tesseract: it catches a class
	// nothing else here checks -- PresentationML schema conformance, with the
	// offending part and XPath -- but it is NOT a substitute for PowerPoint.
	// It accepts the exact package a desktop PowerPoint refused, because the
	// container fields that defect lived in are advisory and .NET's own
	// ZipArchive ignores them too.
	ooxmlStatus := "ok"
	if _, err := engine.LookPath("dotnet"); err != nil {
		ooxmlStatus = "optional, not installed (see tools/ooxml-validate)"
	}
	_, _ = fmt.Fprintf(stdout, "%-9s %-21s %s\n", "ooxml", "dotnet", ooxmlStatus)

	if !allReady {
		_, _ = fmt.Fprintln(stdout, "\nhtml always works; install the tools above for the other formats.")
		return exitError
	}
	return exitOK
}
