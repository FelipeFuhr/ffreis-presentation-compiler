package contract_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/contract"
)

// sampleThemeDir is the shipped theme's root; it ships no contract.yaml of
// its own, so Load must fall back to the compiler's embedded default.
const sampleThemeDir = "../../examples/themes/sample"

func TestLoadFallsBackToTheDefaultWhenAThemeHasNoOverride(t *testing.T) {
	c, err := contract.Load(sampleThemeDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	table, ok := c.Layouts["table"]
	if !ok {
		t.Fatal("default contract has no \"table\" layout")
	}
	if strings.Join(table.Required, ",") != "columns,rows" {
		t.Errorf("table.Required = %v, want [columns rows]", table.Required)
	}
}

func TestLoadReadsAThemesOwnOverride(t *testing.T) {
	dir := t.TempDir()
	override := "layouts:\n  custom:\n    required: [foo]\n    allowed: [bar]\n"
	if err := os.WriteFile(filepath.Join(dir, "contract.yaml"), []byte(override), 0o600); err != nil {
		t.Fatalf("write override: %v", err)
	}

	c, err := contract.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := c.Layouts["table"]; ok {
		t.Error("an override must replace the default wholesale, not merge with it")
	}
	custom, ok := c.Layouts["custom"]
	if !ok {
		t.Fatal("override's \"custom\" layout did not load")
	}
	if strings.Join(custom.Required, ",") != "foo" || strings.Join(custom.Allowed, ",") != "bar" {
		t.Errorf("custom = %+v, want required [foo] allowed [bar]", custom)
	}
}

func TestLoadReportsAMalformedOverride(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "contract.yaml"), []byte("layouts: [not a map"), 0o600); err != nil {
		t.Fatalf("write override: %v", err)
	}

	if _, err := contract.Load(dir); err == nil {
		t.Fatal("want an error for a malformed contract.yaml")
	}
}
