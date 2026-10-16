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
	// A generator that exits 0 without writing is a silent failure; refuse it.
	info, err := os.Stat(abs)
	if err != nil || info.Size() == 0 {
		return "", fmt.Errorf("generator %q (%s): exited cleanly but wrote no output to %s",
			g.Name(), req.Spec.Spec, rel)
	}
	return rel, nil
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
