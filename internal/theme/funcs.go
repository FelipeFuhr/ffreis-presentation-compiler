package theme

import (
	"fmt"
	"html/template"
)

// FuncMap is the vocabulary available to chrome and layout templates. It stays
// small on purpose: YAML front-matter arrives as `any`, so templates mostly
// need coercion helpers, not logic.
func FuncMap() template.FuncMap {
	return template.FuncMap{
		"get":      get,
		"str":      str,
		"int":      toInt,
		"list":     list,
		"rows":     rows,
		"add":      func(a, b int) int { return a + b },
		"default":  defaultValue,
		"safeHTML": func(s string) template.HTML { return template.HTML(s) }, //nolint:gosec // theme-authored markup, same trust boundary as the template itself
	}
}

// get looks a key up in a layout's field map, returning nil when absent so
// templates can use `with`/`if` instead of guarding every access.
func get(m map[string]any, key string) any {
	if m == nil {
		return nil
	}
	return m[key]
}

// str coerces a YAML scalar to a display string.
func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

// toInt coerces a YAML scalar to an int, yielding 0 when absent or not
// numeric. Templates compare loop indices against it, and `eq` would fail on
// mismatched types without this.
func toInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	default:
		return 0
	}
}

// list coerces a YAML sequence to a slice templates can range over.
func list(v any) []any {
	switch t := v.(type) {
	case nil:
		return nil
	case []any:
		return t
	default:
		return []any{t}
	}
}

// rows coerces a YAML sequence of sequences, the shape table layouts use.
func rows(v any) [][]any {
	outer := list(v)
	out := make([][]any, 0, len(outer))
	for _, row := range outer {
		out = append(out, list(row))
	}
	return out
}

// defaultValue returns fallback when v is nil or an empty string.
func defaultValue(fallback, v any) any {
	if v == nil || v == "" {
		return fallback
	}
	return v
}
