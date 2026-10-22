package theme_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/theme"
)

// examplesRoot is the themes directory shipped with the repo; the sample theme
// doubles as a fixture so the examples can never silently rot.
const examplesRoot = "../../examples/themes"

func TestLoadReadsTheSampleTheme(t *testing.T) {
	th, err := theme.Load(examplesRoot, "sample")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if th.Name != "sample" {
		t.Errorf("Name = %q, want sample", th.Name)
	}
	if !strings.Contains(th.CSS, "--accent") {
		t.Error("CSS does not contain the token block")
	}
	if !strings.Contains(th.CSS, ".deck-table") {
		t.Error("CSS is missing layout rules; stylesheets may not all be concatenated")
	}
	if th.Chrome() == nil {
		t.Error("chrome template was not parsed")
	}

	want := []string{"bullets", "cover", "hero-media", "section", "split", "table"}
	got := th.LayoutNames()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("LayoutNames() = %v, want %v", got, want)
	}
}

func TestLayoutLookup(t *testing.T) {
	th, err := theme.Load(examplesRoot, "sample")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if _, err := th.Layout("table"); err != nil {
		t.Errorf("Layout(table): %v", err)
	}

	_, err = th.Layout("nope")
	if err == nil {
		t.Fatal("want an error for an unknown layout")
	}
	// The message must list what *is* available: an unknown layout is the most
	// likely authoring mistake, and guessing the name should not require
	// opening the theme.
	if !strings.Contains(err.Error(), "table") {
		t.Errorf("error %q does not list available layouts", err)
	}
}

func TestLoadRejectsBrokenThemes(t *testing.T) {
	t.Run("unnamed theme", func(t *testing.T) {
		if _, err := theme.Load(examplesRoot, ""); err == nil {
			t.Fatal("want an error when deck.yaml names no theme")
		}
	})

	t.Run("missing directory", func(t *testing.T) {
		if _, err := theme.Load(examplesRoot, "absent"); err == nil {
			t.Fatal("want an error for a theme that does not exist")
		}
	})

	t.Run("theme name contains a path separator", func(t *testing.T) {
		if _, err := theme.Load(examplesRoot, "sample/../sample"); err == nil {
			t.Fatal("want an error: a theme name must be a single directory name")
		}
	})

	t.Run("theme name is a traversal attempt", func(t *testing.T) {
		if _, err := theme.Load(examplesRoot, "../../../etc"); err == nil {
			t.Fatal("want an error: a theme name may not escape the themes root")
		}
	})

	t.Run("theme name is an absolute path", func(t *testing.T) {
		if _, err := theme.Load(examplesRoot, "/etc"); err == nil {
			t.Fatal("want an error: a theme name is a directory name, not a path")
		}
	})

	t.Run("theme path is a file", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "flat"), []byte("x"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := theme.Load(root, "flat"); err == nil {
			t.Fatal("want an error when the theme path is not a directory")
		}
	})

	t.Run("no stylesheets", func(t *testing.T) {
		root := writePartialTheme(t, map[string]string{
			"theme.yaml":           "name: broken\n",
			"slide.gohtml":         "<section></section>",
			"layouts/cover.gohtml": "<div></div>",
		})
		_, err := theme.Load(root, "broken")
		if err == nil || !strings.Contains(err.Error(), "no stylesheets") {
			t.Fatalf("want a \"no stylesheets\" error, got %v", err)
		}
	})

	t.Run("no layouts", func(t *testing.T) {
		root := writePartialTheme(t, map[string]string{
			"theme.yaml":   "name: broken\n",
			"slide.gohtml": "<section></section>",
			"css/base.css": ":root{}",
		})
		_, err := theme.Load(root, "broken")
		if err == nil || !strings.Contains(err.Error(), "no layouts") {
			t.Fatalf("want a \"no layouts\" error, got %v", err)
		}
	})

	t.Run("unparsable template", func(t *testing.T) {
		root := writePartialTheme(t, map[string]string{
			"theme.yaml":           "name: broken\n",
			"slide.gohtml":         "{{ .Unclosed ",
			"css/base.css":         ":root{}",
			"layouts/cover.gohtml": "<div></div>",
		})
		if _, err := theme.Load(root, "broken"); err == nil {
			t.Fatal("want an error for a malformed template")
		}
	})

	t.Run("malformed manifest", func(t *testing.T) {
		root := writePartialTheme(t, map[string]string{
			"theme.yaml":           "name: [unclosed\n",
			"slide.gohtml":         "<section></section>",
			"css/base.css":         ":root{}",
			"layouts/cover.gohtml": "<div></div>",
		})
		if _, err := theme.Load(root, "broken"); err == nil {
			t.Fatal("want an error for a malformed theme.yaml")
		}
	})

	t.Run("missing manifest", func(t *testing.T) {
		root := writePartialTheme(t, map[string]string{"css/base.css": ":root{}"})
		if _, err := theme.Load(root, "broken"); err == nil {
			t.Fatal("want an error for a theme with no theme.yaml")
		}
	})
}

// writePartialTheme builds a theme named "broken" from the given relative
// paths and returns the root to load it from.
func writePartialTheme(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		path := filepath.Join(root, "broken", rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

func TestAssetAndFontDirsHangOffTheThemeDir(t *testing.T) {
	th, err := theme.Load(examplesRoot, "sample")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if filepath.Base(th.AssetsDir()) != "assets" {
		t.Errorf("AssetsDir = %q", th.AssetsDir())
	}
	if filepath.Base(th.FontsDir()) != "fonts" {
		t.Errorf("FontsDir = %q", th.FontsDir())
	}
}

func TestTokensExtractsRootCustomProperties(t *testing.T) {
	th, err := theme.Load(examplesRoot, "sample")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	tokens := th.Tokens()

	if got := tokens["accent"]; got != "#2563eb" {
		t.Errorf("tokens[accent] = %q, want #2563eb", got)
	}
	if _, ok := tokens["font-body"]; !ok {
		t.Error("tokens is missing font-body")
	}
	// Leading dashes are stripped so a generator sees plain names.
	if _, leaked := tokens["--accent"]; leaked {
		t.Error("token names should not keep their leading dashes")
	}
	// Only :root declarations are tokens; a value scoped to a selector is the
	// theme saying "this is not shared".
	if _, leaked := tokens["marketing-only"]; leaked {
		t.Error("a non-:root custom property leaked into tokens")
	}
}

func TestTokensOnTooHandlesTypicalShapes(t *testing.T) {
	root := writePartialTheme(t, map[string]string{
		"theme.yaml":       "name: broken\n",
		"slide.gohtml":     "<section></section>",
		"css/base.css":     ":root{--a:1px;--b: two words ;}\n.scoped{--c:3;}\n:root { --d: #fff; }",
		"layouts/x.gohtml": "<div></div>",
	})
	th, err := theme.Load(root, "broken")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	tokens := th.Tokens()

	for name, want := range map[string]string{"a": "1px", "b": "two words", "d": "#fff"} {
		if tokens[name] != want {
			t.Errorf("tokens[%s] = %q, want %q", name, tokens[name], want)
		}
	}
	if _, leaked := tokens["c"]; leaked {
		t.Error("a selector-scoped property was treated as a token")
	}
}
