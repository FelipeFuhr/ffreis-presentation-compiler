package theme_test

import (
	"bytes"
	"html/template"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/theme"
)

// render executes a snippet against the real FuncMap, which is how layouts use
// these helpers — testing them through the template engine rather than as bare
// functions keeps the test honest about what a theme author can rely on.
func render(t *testing.T, src string, data any) string {
	t.Helper()
	tpl, err := template.New("t").Funcs(theme.FuncMap()).Parse(src)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute %q: %v", src, err)
	}
	return buf.String()
}

func TestTemplateHelpers(t *testing.T) {
	fields := map[string]any{
		"title":   "Hello",
		"count":   3,
		"ratio":   2.5,
		"items":   []any{"a", "b"},
		"rows":    []any{[]any{"r1c1", "r1c2"}, []any{"r2c1"}},
		"single":  "lonely",
		"blank":   "",
		"missing": nil,
	}
	data := map[string]any{"Fields": fields}

	cases := []struct {
		name string
		src  string
		want string
	}{
		{"get and str", `{{ str (get .Fields "title") }}`, "Hello"},
		{"str on a number", `{{ str (get .Fields "count") }}`, "3"},
		{"str on absent key", `{{ str (get .Fields "nope") }}`, ""},
		{"int", `{{ int (get .Fields "count") }}`, "3"},
		{"int truncates a float", `{{ int (get .Fields "ratio") }}`, "2"},
		{"int on a non-number", `{{ int (get .Fields "title") }}`, "0"},
		{"list ranges", `{{ range list (get .Fields "items") }}{{ str . }}{{ end }}`, "ab"},
		{"list wraps a scalar", `{{ range list (get .Fields "single") }}{{ str . }}{{ end }}`, "lonely"},
		{"list on absent key", `{{ range list (get .Fields "nope") }}x{{ end }}`, ""},
		{"rows ranges rows and cells", `{{ range rows (get .Fields "rows") }}[{{ range . }}{{ str . }}{{ end }}]{{ end }}`, "[r1c1r1c2][r2c1]"},
		{"add", `{{ add 2 3 }}`, "5"},
		{"default falls back on nil", `{{ default "fallback" (get .Fields "missing") }}`, "fallback"},
		{"default falls back on empty", `{{ default "fallback" (get .Fields "blank") }}`, "fallback"},
		{"default keeps a value", `{{ default "fallback" (get .Fields "title") }}`, "Hello"},
		{"safeHTML does not escape", `{{ safeHTML "<b>x</b>" }}`, "<b>x</b>"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := render(t, tc.src, data); got != tc.want {
				t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
			}
		})
	}
}

func TestGetOnNilMapIsSafe(t *testing.T) {
	// Layouts call `get .Fields "x"` on slides whose front-matter had no
	// layout keys at all, so Fields can legitimately be nil.
	if got := render(t, `{{ str (get .Fields "anything") }}`, map[string]any{"Fields": map[string]any(nil)}); got != "" {
		t.Errorf("get on a nil map = %q, want empty", got)
	}
}
