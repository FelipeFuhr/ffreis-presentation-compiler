package pptx_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/engine/pptx"
)

// observed collects every rule name any finding in this suite carried. The
// rules helper in verify_test.go records into it, so every existing assertion
// contributes without being rewritten.
var observed = map[string]bool{}

// TestMain runs the suite, then asserts it exercised every rule.
//
// This is the gate that makes Rules() worth having. Eighteen rules were
// checked by hand once; a nineteenth added with no fixture would have shipped
// silently, and a verifier that is not itself verified is just an assertion
// that the author was careful.
func TestMain(m *testing.M) {
	code := m.Run()
	if code == 0 {
		if missing := unexercised(); len(missing) > 0 {
			println("no fixture in this suite makes these rules fire: " + strings.Join(missing, " "))
			code = 1
		}
	}
	os.Exit(code)
}

func unexercised() []string {
	var missing []string
	for _, r := range pptx.Rules() {
		if !observed[r] {
			missing = append(missing, r)
		}
	}
	sort.Strings(missing)
	return missing
}

// TestRulesListsEveryRuleConstant reads the package's own source and checks
// that Rules() names every exported Rule* constant.
//
// A handwritten list can drift from the constants it mirrors, which would
// reintroduce the gap one level up: the completeness gate above can only
// police rules it is told about.
func TestRulesListsEveryRuleConstant(t *testing.T) {
	listed := map[string]bool{}
	for _, r := range pptx.Rules() {
		listed[r] = true
	}

	for name, value := range ruleConstants(t) {
		if !listed[value] {
			t.Errorf("%s = %q is not in Rules(), so no test can be required to exercise it", name, value)
		}
	}
}

// ruleConstants parses the non-test sources of package pptx and returns every
// exported constant whose name begins with "Rule", mapped to its value.
//
// Files are enumerated and parsed one at a time rather than through
// parser.ParseDir, which is deprecated for not honouring build tags. This
// package has no build-tagged files, and the alternative -- go/packages --
// would add a dependency and a type-check to answer a question that is purely
// syntactic.
func ruleConstants(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}

	fset := token.NewFileSet()
	out := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		collectRuleConsts(file, out)
	}
	if len(out) == 0 {
		t.Fatal("found no Rule* constants, so this test proves nothing")
	}
	return out
}

// collectRuleConsts records every Rule* string constant declared in one file.
func collectRuleConsts(file *ast.File, out map[string]string) {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			collectRuleConst(spec, out)
		}
	}
}

func collectRuleConst(spec ast.Spec, out map[string]string) {
	vs, ok := spec.(*ast.ValueSpec)
	if !ok {
		return
	}
	for i, name := range vs.Names {
		if !strings.HasPrefix(name.Name, "Rule") || i >= len(vs.Values) {
			continue
		}
		lit, ok := vs.Values[i].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		out[name.Name] = strings.Trim(lit.Value, `"`)
	}
}
