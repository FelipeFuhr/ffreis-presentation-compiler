package gen

import "os/exec"

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
