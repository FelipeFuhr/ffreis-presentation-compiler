// Package cli is the command surface. Dispatch is handwritten on os.Args[1],
// matching the other compilers in this fleet, and Run is split from run so the
// exit-code path is testable without os.Exit.
package cli

import (
	"fmt"
	"io"
	"os"
)

// ExitCode values the binary returns.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// Run executes the CLI and terminates the process.
func Run() {
	os.Exit(run(os.Args[0], os.Args[1:], os.Stdout, os.Stderr))
}

// run is the testable core: it never calls os.Exit and writes nothing to the
// real stdout/stderr unless handed them.
func run(program string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr, program)
		return exitUsage
	}

	switch args[0] {
	case "build", "compile":
		return runBuild(args[1:], stdout, stderr)
	case "validate":
		return runValidate(args[1:], stdout, stderr)
	case "check":
		return runCheck(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		usage(stdout, program)
		return exitOK
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		usage(stderr, program)
		return exitUsage
	}
}

func usage(w io.Writer, program string) {
	_, _ = fmt.Fprintf(w, `%s — compiles declarative slide decks to HTML, PDF and PNG.

usage:
  %s build     -deck-root <dir> -theme-root <dir> [-out dist] [-formats html,pdf]
  %s validate  -deck-root <dir> -theme-root <dir>
  %s check     [-dir dist] [-theme-root <dir> -theme <name>]
  %s doctor

commands:
  build     render a deck directory into the output directory
  validate  check a deck against its theme's field contract before rendering
  check     measure a rendered deck: overflow, out-of-bounds, contrast, fonts
  doctor    report which render adapters are available on this machine
`, program, program, program, program, program)
}
