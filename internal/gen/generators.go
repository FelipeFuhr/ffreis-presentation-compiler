package gen

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// LookPath is indirected so tests can simulate a missing toolchain.
var LookPath = func(name string) (string, error) { return exec.LookPath(name) }

func available(name string) bool {
	_, err := LookPath(name)
	return err == nil
}

// The generator contract, honoured by python and cmd:
//
//	<tool> <spec> --out <path> --tokens <json> [--data <path>] [extra args…]
//
// A generator reads --tokens to colour its output with the deck's theme, and
// must write the file at --out.
func contractArgs(outPath, tokensJSON, dataPath string, extra []string) []string {
	args := []string{"--out", outPath, "--tokens", tokensJSON}
	if dataPath != "" {
		args = append(args, "--data", dataPath)
	}
	return append(args, extra...)
}

// ── python ──────────────────────────────────────────────────────────────────

type pythonGenerator struct{}

var python Generator = pythonGenerator{}

func (pythonGenerator) Name() string    { return "python" }
func (pythonGenerator) Tool() string    { return "python3" }
func (pythonGenerator) Ext() string     { return "svg" }
func (pythonGenerator) Available() bool { return available("python3") }
func (g pythonGenerator) Command(specPath, dataPath, outPath, tokensJSON string, extra []string) (string, []string) {
	return g.Tool(), append([]string{specPath}, contractArgs(outPath, tokensJSON, dataPath, extra)...)
}

// ── cmd: any executable spec honouring the same contract ────────────────────

type cmdGenerator struct{}

var command Generator = cmdGenerator{}

func (cmdGenerator) Name() string    { return "cmd" }
func (cmdGenerator) Tool() string    { return "" } // the spec file is the tool
func (cmdGenerator) Ext() string     { return "svg" }
func (cmdGenerator) Available() bool { return true }
func (cmdGenerator) Command(specPath, dataPath, outPath, tokensJSON string, extra []string) (string, []string) {
	return specPath, contractArgs(outPath, tokensJSON, dataPath, extra)
}

// ── mermaid ─────────────────────────────────────────────────────────────────

// mermaidGenerator shells out to mermaid-cli. It does not take --tokens:
// mmdc themes through its own config file, so a themed diagram needs a
// generator-level config rather than the shared contract. Diagrams therefore
// inherit the brand less automatically than python charts do.
type mermaidGenerator struct{}

var mermaid Generator = mermaidGenerator{}

func (mermaidGenerator) Name() string    { return "mermaid" }
func (mermaidGenerator) Tool() string    { return "mmdc" }
func (mermaidGenerator) Ext() string     { return "svg" }
func (mermaidGenerator) Available() bool { return available("mmdc") }
func (g mermaidGenerator) Command(specPath, _, outPath, _ string, extra []string) (string, []string) {
	return g.Tool(), append([]string{"-i", specPath, "-o", outPath, "-b", "transparent"}, extra...)
}

// ── d2 ──────────────────────────────────────────────────────────────────────

// d2ThemeSlots maps the compiler's own generic token vocabulary — the names
// the shipped example theme uses (internal/theme.Tokens reads a theme's
// `:root` CSS custom properties verbatim, so this is deliberately a small,
// documented convention rather than something tied to any one theme) — onto
// D2's theme-override slots. Ordered, not a map, so InjectTokens emits the
// vars block in a stable order and the generated spec is deterministic.
//
// D2 has no CLI flag or environment variable for custom colours: overriding
// a slot only takes effect from a `vars: { d2-config: { theme-overrides: {
// … } } }` block in the diagram's own source (verified against d2 v0.7.1).
// So, uniquely among this package's generators, token injection happens by
// rewriting the spec before invocation rather than by an argument — see
// InjectTokens and gen.go's use of the TokenInjector interface.
var d2ThemeSlots = []struct{ token, slot string }{
	{"accent", "B1"},       // primary shape fill and edges
	{"accent-text", "N1"},  // darkest neutral: text and strokes
	{"paper", "N7"},        // lightest neutral: diagram background
	{"paper-strong", "N6"}, // secondary background
}

// hexColor matches a plain #rgb or #rrggbb value. D2 accepts only a named
// colour or a hex code in a theme-overrides slot — not rgba() or any other
// CSS function — and rejects anything else with a compile error naming the
// slot, so a token in that shape (an author's soft/alpha accent, say) is
// filtered out here rather than handed to d2 to fail on.
var hexColor = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

type d2Generator struct{}

var d2 Generator = d2Generator{}

func (d2Generator) Name() string    { return "d2" }
func (d2Generator) Tool() string    { return "d2" }
func (d2Generator) Ext() string     { return "svg" }
func (d2Generator) Available() bool { return available("d2") }

// Command takes positional arguments (input, output), unlike the --flag
// contract the other generators share — d2's own CLI shape, not this
// package's. --data has no d2 equivalent and is intentionally unused: a
// diagram's nodes and edges are the spec itself, not data driving a chart.
func (g d2Generator) Command(specPath, _, outPath, _ string, extra []string) (string, []string) {
	return g.Tool(), append([]string{specPath, outPath}, extra...)
}

// InjectTokens prepends a theme-overrides vars block built from whichever of
// d2ThemeSlots are present and hex-valued, so a D2 diagram picks up the
// deck's brand colours without the author hand-maintaining them in the
// diagram source. Deterministic and order-stable, because gen.go's content
// hash still covers the original spec bytes plus --tokens separately rather
// than the rewritten file — the ordering matters for the rewritten file
// itself to be reproducible, not for cache correctness.
func (d2Generator) InjectTokens(spec []byte, tokens map[string]string) ([]byte, error) {
	var b strings.Builder
	for _, m := range d2ThemeSlots {
		val, ok := tokens[m.token]
		if !ok || !hexColor.MatchString(val) {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("vars: {\n  d2-config: {\n    theme-overrides: {\n")
		}
		fmt.Fprintf(&b, "      %s: %q\n", m.slot, val)
	}
	if b.Len() == 0 {
		return spec, nil
	}
	b.WriteString("    }\n  }\n}\n")
	return append([]byte(b.String()), spec...), nil
}
