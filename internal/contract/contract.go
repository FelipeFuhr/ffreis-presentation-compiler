// Package contract validates a loaded deck against a theme's field
// vocabulary before anything renders.
//
// Today deck.Load fails only on a missing "layout:" key; a bad field beyond
// that reaches a Go template and produces either a blank region or a
// confusing template error. Validate turns every such problem into a named,
// upfront finding instead — naming the slide's source file and the
// offending key — and reports all of them in one pass rather than stopping
// at the first.
//
// A theme's field vocabulary is declarative, the same idiom as the
// compiler's own chrome/Fields split: a layout lists the keys it requires
// and the keys it additionally allows, and any other key is an error. The
// compiler ships a default contract describing the sample theme's layouts; a
// theme overrides it wholesale with its own contract.yaml at its root, so
// adding a layout is a YAML change, not a Go change.
package contract

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// overrideFile is the theme-root file that replaces the shipped default.
const overrideFile = "contract.yaml"

//go:embed contract.yaml
var defaultContractYAML []byte

// Layout is the field vocabulary for one layout: the keys a slide using it
// must set, and the keys it may additionally set. Any other key is an error.
type Layout struct {
	Required []string `yaml:"required"`
	Allowed  []string `yaml:"allowed"`
}

// Contract is a theme's full field vocabulary, one Layout per layout name.
type Contract struct {
	Layouts map[string]Layout `yaml:"layouts"`
}

// defaultContract is the compiler's own shipped vocabulary, parsed once at
// package initialisation: a parse failure here would be a bug in this repo's
// embedded file, not operator input, so it panics rather than threading an
// error through every caller.
var defaultContract = mustParseContract(defaultContractYAML)

func mustParseContract(raw []byte) Contract {
	c, err := parseContract(raw)
	if err != nil {
		panic("internal/contract: embedded contract.yaml is invalid: " + err.Error())
	}
	return c
}

func parseContract(raw []byte) (Contract, error) {
	var c Contract
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return Contract{}, err
	}
	return c, nil
}

// Load returns the field contract a theme uses: its own contract.yaml at
// themeDir, or the compiler's shipped default when the theme does not
// provide one.
func Load(themeDir string) (Contract, error) {
	path := filepath.Join(themeDir, overrideFile)
	raw, err := os.ReadFile(path) //nolint:gosec // theme root is an operator-supplied flag
	if errors.Is(err, os.ErrNotExist) {
		return defaultContract, nil
	}
	if err != nil {
		return Contract{}, fmt.Errorf("read %s: %w", path, err)
	}
	c, err := parseContract(raw)
	if err != nil {
		return Contract{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return c, nil
}

// known reports the field names a layout recognises: required and allowed
// together, so a contract author never lists a key twice.
func (l Layout) known() map[string]bool {
	names := make(map[string]bool, len(l.Required)+len(l.Allowed))
	for _, n := range l.Required {
		names[n] = true
	}
	for _, n := range l.Allowed {
		names[n] = true
	}
	return names
}
