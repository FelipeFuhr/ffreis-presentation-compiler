package theme

import (
	"regexp"
	"strings"
)

// rootBlock matches a `:root { … }` rule; a theme may declare several.
var rootBlock = regexp.MustCompile(`(?s):root\s*\{(.*?)\}`)

// customProperty matches one `--name: value;` declaration.
var customProperty = regexp.MustCompile(`--([A-Za-z0-9_-]+)\s*:\s*([^;]+);`)

// Tokens returns the theme's CSS custom properties, keyed without the leading
// dashes. They are handed to chart and diagram generators so generated images
// inherit the brand instead of hardcoding their own palette.
//
// This reads only `:root` blocks: a token is something the whole deck shares,
// and scoping a value to a selector is how a theme says "not a token".
func (t *Theme) Tokens() map[string]string {
	tokens := map[string]string{}
	for _, block := range rootBlock.FindAllStringSubmatch(t.CSS, -1) {
		for _, decl := range customProperty.FindAllStringSubmatch(block[1], -1) {
			tokens[decl[1]] = strings.TrimSpace(decl[2])
		}
	}
	return tokens
}
