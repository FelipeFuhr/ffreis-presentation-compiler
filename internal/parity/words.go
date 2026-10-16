package parity

import (
	"regexp"
	"strings"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
)

// minSignificantWordLen is the shortest word this package bothers to check
// for. OCR mangles short, common words the most — single- and double-letter
// fragments, stray punctuation misread as a glyph — and they carry the least
// content signal: nobody would call a slide broken because "de" or "um" did
// not survive OCR. Excluding them keeps every slide's report about words
// that actually matter instead of noise.
const minSignificantWordLen = 4

// nonVisualFieldKeys are front-matter keys this package knows never paint
// literal text on a slide, so their string values are not "expected" text:
// alt is accessibility metadata (read by a screen reader, not rendered),
// media is an asset path, and chart/diagram are generator config blocks —
// including their own args, which are tool flags and values (a chart title,
// a layout name) this package cannot reliably tell apart positionally. The
// image a generator produces is real rendered content, but comparing it
// against the deck's own source text is out of scope: the compiler does not
// know what a generator wrote into that image.
var nonVisualFieldKeys = map[string]bool{"alt": true, "media": true, "chart": true, "diagram": true}

// wordPattern tokenizes already-folded text (normalize has lowercased and
// stripped diacritics, so only ASCII letters and digits remain meaningful);
// anything else — hyphens, punctuation, markdown left over from a naive tag
// strip — is a separator.
var wordPattern = regexp.MustCompile(`[a-z0-9]+`)

// htmlTag strips markup from a slide's rendered Markdown body. The body is
// compiler-rendered goldmark output, the same trust boundary as a template,
// so a blunt strip is enough — this is not parsing untrusted HTML.
var htmlTag = regexp.MustCompile(`<[^>]*>`)

// diacriticFold strips the diacritics a presentation is plausibly authored
// with — the Romance and Germanic languages this compiler's decks target —
// so "Função" and "Funcao" compare equal regardless of which side (the
// source, or OCR misreading the wrong language model) carries the accent.
// Hand-rolled rather than a Unicode normalization package: the set of marks
// in scope is small and known in advance, and a script family this does not
// cover (Cyrillic, CJK, Arabic) would not tokenize into whitespace-separated
// "words" by this package's method anyway, so a general NFKD dependency
// would not buy real support, only an unused one.
var diacriticFold = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a", "å", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "õ", "o", "ö", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ç", "c", "ñ", "n", "ý", "y", "ÿ", "y",
	"ß", "ss",
)

// normalize folds s to lowercase and strips the diacritics diacriticFold
// knows, the common ground both an authored string and an OCR transcript are
// compared on.
func normalize(s string) string {
	return diacriticFold.Replace(strings.ToLower(s))
}

// significantWords tokenizes already-normalized text into the set of words
// at least minSignificantWordLen long.
func significantWords(normalized string) map[string]bool {
	words := map[string]bool{}
	for _, w := range wordPattern.FindAllString(normalized, -1) {
		if len(w) >= minSignificantWordLen {
			words[w] = true
		}
	}
	return words
}

// ExpectedWords returns the normalized, significant vocabulary a slide's own
// source declares: its visible chrome (kicker, title, phase — Badge and
// Notes are deliberately excluded, a path and speaker notes respectively,
// neither painted on the slide itself), its rendered body prose, and every
// string value in its layout fields other than nonVisualFieldKeys.
func ExpectedWords(s deck.Slide) map[string]bool {
	words := map[string]bool{}
	add := func(text string) {
		for w := range significantWords(normalize(text)) {
			words[w] = true
		}
	}

	add(s.Kicker)
	add(s.Title)
	add(s.Phase)
	add(htmlTag.ReplaceAllString(string(s.Body), " "))
	walkFields(s.Fields, add)
	return words
}

// walkFields visits every string leaf in a slide's layout fields, the same
// shape internal/theme's template funcs coerce YAML into: a sequence is
// []any, a mapping is map[string]any, everything else is a scalar.
func walkFields(fields map[string]any, add func(string)) {
	for k, v := range fields {
		if nonVisualFieldKeys[k] {
			continue
		}
		walkValue(v, add)
	}
}

func walkValue(v any, add func(string)) {
	switch t := v.(type) {
	case string:
		add(t)
	case []any:
		for _, item := range t {
			walkValue(item, add)
		}
	case map[string]any:
		for k, item := range t {
			if nonVisualFieldKeys[k] {
				continue
			}
			walkValue(item, add)
		}
	}
}
