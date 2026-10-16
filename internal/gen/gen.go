// Package gen runs content generators — the hook that turns a chart or diagram
// spec into an image at build time.
//
// The point of the hook is the token injection: a generator is handed the
// theme's design tokens, so a chart inherits the brand automatically instead of
// being a screenshot that drifts from the deck around it.
//
// Output is content-addressed. The cache key covers the spec, its data and the
// tokens, so editing any of the three regenerates and editing none of them
// costs nothing.
package gen

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/pathsafe"
)

// Dir is the subdirectory of the output directory that generated files land in.
const Dir = "generated"

// Spec is a generator invocation declared in a slide's front-matter.
type Spec struct {
	Generator string   `yaml:"generator"`
	Spec      string   `yaml:"spec"`
	Data      string   `yaml:"data"`
	Args      []string `yaml:"args"`
}

// Request is one resolved generation job.
type Request struct {
	Spec     Spec
	DeckRoot string
	OutDir   string
	Tokens   map[string]string
}

// Generator builds the command line for one kind of spec. Keeping command
// construction separate from execution is what lets every generator be tested
// without the underlying tool installed.
type Generator interface {
	Name() string
	Tool() string
	Available() bool
	// Ext is the output file extension, without the dot.
	Ext() string
	Command(specPath, dataPath, outPath, tokensJSON string, args []string) (string, []string)
}

// Runner executes an external command; injected so tests need no toolchain.
type Runner func(ctx context.Context, name string, args ...string) error

// TokenInjector is implemented by a generator whose tool has no runtime flag
// for custom colours, so tokens can only reach it by rewriting the spec file
// itself before invocation. Resolve calls this, when the chosen generator
// implements it, on a cache miss only — spec/data bytes and tokensJSON are
// still what the cache key hashes (see fingerprint), so this never changes
// what counts as a cache hit, only what gets written to disk and handed to
// Command on a miss.
type TokenInjector interface {
	InjectTokens(spec []byte, tokens map[string]string) ([]byte, error)
}

// Registry maps generator names to implementations.
type Registry map[string]Generator

// DefaultRegistry is the set of generators the compiler ships.
func DefaultRegistry() Registry {
	return Registry{
		python.Name():  python,
		command.Name(): command,
		mermaid.Name(): mermaid,
		d2.Name():      d2,
	}
}

// Names lists the registered generators, sorted.
func (r Registry) Names() []string {
	names := make([]string, 0, len(r))
	for n := range r {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Resolve produces the artifact for req and returns its path relative to the
// output directory, suitable for a src attribute.
func (r Registry) Resolve(ctx context.Context, run Runner, req Request) (string, error) {
	g, ok := r[req.Spec.Generator]
	if !ok {
		return "", fmt.Errorf("unknown generator %q: want one of %v", req.Spec.Generator, r.Names())
	}
	if req.Spec.Spec == "" {
		return "", fmt.Errorf("generator %q: no spec file given", g.Name())
	}
	if !g.Available() {
		return "", fmt.Errorf("generator %q needs %q on PATH", g.Name(), g.Tool())
	}

	// Contain is the only thing standing between a deck author's spec/data
	// path and a read outside the deck root; filepath.Join alone would follow
	// a ".." straight out of it.
	specPath, err := pathsafe.Contain(req.DeckRoot, req.Spec.Spec)
	if err != nil {
		return "", fmt.Errorf("generator %q: %w", g.Name(), err)
	}
	specBytes, err := os.ReadFile(specPath) //nolint:gosec // contained within the operator's own deck root, checked above
	if err != nil {
		return "", fmt.Errorf("generator %q: %w", g.Name(), err)
	}

	var dataPath string
	var dataBytes []byte
	if req.Spec.Data != "" {
		if dataPath, err = pathsafe.Contain(req.DeckRoot, req.Spec.Data); err != nil {
			return "", fmt.Errorf("generator %q: %w", g.Name(), err)
		}
		if dataBytes, err = os.ReadFile(dataPath); err != nil { //nolint:gosec // as above
			return "", fmt.Errorf("generator %q: %w", g.Name(), err)
		}
	}

	tokensJSON, err := encodeTokens(req.Tokens)
	if err != nil {
		return "", fmt.Errorf("generator %q: %w", g.Name(), err)
	}

	name := fingerprint(g, specBytes, dataBytes, tokensJSON, req.Spec.Args) + "." + g.Ext()
	rel := filepath.Join(Dir, name)
	abs := filepath.Join(req.OutDir, rel)

	if _, err := os.Stat(abs); err == nil {
		return rel, nil // cache hit: identical spec, data and tokens
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		return "", fmt.Errorf("generator %q: %w", g.Name(), err)
	}

	specForCmd, cleanup, err := prepareSpec(g, specPath, specBytes, req.Tokens)
	if err != nil {
		return "", fmt.Errorf("generator %q: %w", g.Name(), err)
	}
	defer cleanup()

	bin, args := g.Command(specForCmd, dataPath, abs, tokensJSON, req.Spec.Args)
	if err := run(ctx, bin, args...); err != nil {
		return "", fmt.Errorf("generator %q (%s): %w", g.Name(), req.Spec.Spec, err)
	}

	// A generator that exits 0 without writing a legitimate artifact is a
	// silent failure; refuse it. The file is read once here and reused for
	// both the emptiness check and the format sniff below.
	data, err := os.ReadFile(abs) //nolint:gosec // this package's own content-addressed output path, not operator input
	if err != nil || len(data) == 0 {
		_ = os.Remove(abs) // never leave a 0-byte write cached at its content hash
		return "", fmt.Errorf("generator %q (%s): exited cleanly but wrote no output to %s",
			g.Name(), req.Spec.Spec, rel)
	}
	if err := validateArtifact(g.Ext(), data); err != nil {
		// Same reasoning as above: the cache-hit check at the top of this
		// function keys only on (generator, spec, data, tokens), so a broken
		// artifact left on disk would make every future build with the same
		// inputs silently "succeed" by reusing it.
		_ = os.Remove(abs)
		return "", fmt.Errorf("generator %q (%s): %s: %w", g.Name(), req.Spec.Spec, rel, err)
	}
	return rel, nil
}

// MinArtifactBytes is the smallest a generated artifact is allowed to be
// before it counts as a legitimate diagram or chart rather than a near-empty
// write — an `<svg/>` root with no children, a truncated partial write — that
// a crashed or misconfigured generator can still exit 0 for. Comfortably
// under anything a real generator produces (the smallest real fixture this
// repo has rendered, a two-node d2 diagram, is in the tens of thousands of
// bytes) and comfortably over what a degenerate write reaches, so this is not
// a threshold real output could plausibly trip by being unusually small.
const MinArtifactBytes = 256

// pngSignature is the fixed 8-byte header every PNG file starts with.
var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// validateArtifact checks data is plausibly real content for ext, not merely
// non-empty. Format sniffing is deliberately shallow — this defends against a
// generator that crashed or misconfigured partway through (wrote an error
// message, a degenerate empty root element, a truncated stream), not against
// a trusted tool producing subtly wrong output, which this package has no way
// to judge and content-parity-style checks exist for instead.
func validateArtifact(ext string, data []byte) error {
	if len(data) < MinArtifactBytes {
		return fmt.Errorf("wrote only %d byte(s), want at least %d — looks like a truncated or near-empty artifact",
			len(data), MinArtifactBytes)
	}
	switch ext {
	case "svg":
		if !bytes.Contains(data, []byte("<svg")) || !bytes.Contains(data, []byte("</svg>")) {
			return fmt.Errorf("does not look like SVG: no <svg>…</svg> element")
		}
	case "png":
		if !bytes.HasPrefix(data, pngSignature) {
			return fmt.Errorf("does not start with a PNG signature")
		}
	}
	return nil
}

// prepareSpec returns the spec path Command should receive: the original, or
// (when g implements TokenInjector) a temp file holding spec rewritten to
// embed tokens. The caller must always call cleanup, which is a no-op when
// no rewrite happened.
func prepareSpec(g Generator, specPath string, spec []byte, tokens map[string]string) (string, func(), error) {
	inj, ok := g.(TokenInjector)
	if !ok {
		return specPath, func() {}, nil
	}
	merged, err := inj.InjectTokens(spec, tokens)
	if err != nil {
		return "", func() {}, err
	}
	tmp, err := os.CreateTemp("", "gen-*-"+filepath.Base(specPath))
	if err != nil {
		return "", func() {}, err
	}
	if _, err := tmp.Write(merged); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", func() {}, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return "", func() {}, err
	}
	return tmp.Name(), func() { _ = os.Remove(tmp.Name()) }, nil
}

// encodeTokens serialises tokens deterministically so the cache key is stable.
func encodeTokens(tokens map[string]string) (string, error) {
	if tokens == nil {
		tokens = map[string]string{}
	}
	b, err := json.Marshal(tokens)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// fingerprint is the content address of one generation job.
func fingerprint(g Generator, spec, data []byte, tokensJSON string, args []string) string {
	h := sha256.New()
	write := func(parts ...string) {
		for _, p := range parts {
			_, _ = h.Write([]byte(p))
			_, _ = h.Write([]byte{0})
		}
	}
	write(g.Name(), g.Ext(), tokensJSON)
	write(args...)
	_, _ = h.Write(spec)
	_, _ = h.Write(data)
	return hex.EncodeToString(h.Sum(nil))[:16]
}
