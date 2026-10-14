package pathsafe_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/pathsafe"
)

func TestContainAcceptsALegitimateNestedPath(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "a", "deck")
	got, err := pathsafe.Contain(root, filepath.Join("assets", "img.png"))
	if err != nil {
		t.Fatalf("Contain: %v", err)
	}
	want := filepath.Join(root, "assets", "img.png")
	if got != want {
		t.Errorf("Contain() = %q, want %q", got, want)
	}
}

func TestContainRejectsDotDotTraversal(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "a", "deck")
	_, err := pathsafe.Contain(root, filepath.Join("..", "..", "etc", "passwd"))
	if err == nil {
		t.Fatal("want an error for a path that escapes root via ..")
	}
	if !strings.Contains(err.Error(), "escapes") {
		t.Errorf("error %q does not say the path escapes the root", err)
	}
}

func TestContainRejectsAnAbsolutePath(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "a", "deck")
	abs := filepath.Join(string(filepath.Separator), "etc", "passwd")
	_, err := pathsafe.Contain(root, abs)
	if err == nil {
		t.Fatal("want an error for an absolute candidate")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Errorf("error %q does not say the path is absolute", err)
	}
}

// TestContainRejectsASiblingDirectoryThatSharesAStringPrefix proves the fix
// for the classic strings.HasPrefix bug: root "/a/deck" is a string prefix of
// the unrelated sibling "/a/deckx", so a naive prefix check on the joined
// absolute path would wrongly accept it. filepath.Rel does not make that
// mistake because it compares path components, not raw strings.
func TestContainRejectsASiblingDirectoryThatSharesAStringPrefix(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "a", "deck")
	// "../deckx/f" relative to root resolves to "/a/deckx/f" — a real sibling
	// directory one level up, not a subdirectory of root.
	candidate := filepath.Join("..", "deckx", "f")
	_, err := pathsafe.Contain(root, candidate)
	if err == nil {
		t.Fatal("want an error: the sibling directory shares root's string prefix but is not contained in it")
	}
}

func TestContainRejectsAnEmptyPath(t *testing.T) {
	if _, err := pathsafe.Contain("/a/deck", ""); err == nil {
		t.Fatal("want an error for an empty candidate")
	}
}
