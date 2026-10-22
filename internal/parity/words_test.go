package parity_test

import (
	"html/template"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/deck"
	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/parity"
)

func words(t *testing.T, s deck.Slide) map[string]bool {
	t.Helper()
	return parity.ExpectedWords(s)
}

func TestExpectedWordsCollectsChromeBodyAndFields(t *testing.T) {
	s := deck.Slide{
		Kicker: "Exemplo de projeto simulado",
		Title:  "Carta de Projeto",
		Phase:  "Definição",
		Body:   template.HTML("<p>Some <strong>prose</strong> here</p>"),
		Fields: map[string]any{
			"items": []any{"Equipe", "Indicadores"},
		},
	}
	got := words(t, s)
	for _, want := range []string{"exemplo", "projeto", "carta", "definicao", "prose", "equipe", "indicadores"} {
		if !got[want] {
			t.Errorf("ExpectedWords is missing %q, got %v", want, got)
		}
	}
}

func TestExpectedWordsExcludesNonVisualFields(t *testing.T) {
	s := deck.Slide{
		Badge: "assets/badge_gremio.png",
		Notes: "Remember to mention the quarterly numbers here",
		Fields: map[string]any{
			"alt":   "A chart showing quarterly growth",
			"media": "assets/generated/abcd1234.svg",
			"chart": map[string]any{
				"generator": "python",
				"spec":      "charts/bars.py",
				"args":      []any{"--title", "Secretive internal codename"},
			},
		},
	}
	got := words(t, s)
	for _, forbidden := range []string{"chart", "showing", "quarterly", "growth", "secretive", "codename", "badge", "gremio", "generated", "python"} {
		if got[forbidden] {
			t.Errorf("ExpectedWords leaked non-visual content %q into %v", forbidden, got)
		}
	}
}

func TestExpectedWordsSkipsShortWords(t *testing.T) {
	s := deck.Slide{Title: "De um ao rio"} // every word here is <4 letters
	got := words(t, s)
	if len(got) != 0 {
		t.Errorf("ExpectedWords(%q) = %v, want no significant words", s.Title, got)
	}
}

func TestExpectedWordsFoldsDiacriticsSoAccentedAndPlainMatch(t *testing.T) {
	accented := words(t, deck.Slide{Title: "Função no Projeto"})
	plain := words(t, deck.Slide{Title: "Funcao no Projeto"})
	for w := range accented {
		if !plain[w] {
			t.Errorf("accented form produced %q, which the plain form does not also produce: %v vs %v", w, accented, plain)
		}
	}
}

func TestExpectedWordsWalksNestedTableRows(t *testing.T) {
	s := deck.Slide{
		Fields: map[string]any{
			"columns": []any{"Empresa", "Retorno"},
			"rows": []any{
				[]any{"Siemens", "R$ 72 milhões"},
				[]any{"Gerdau", "R$ 150 milhões"},
			},
		},
	}
	got := words(t, s)
	for _, want := range []string{"siemens", "gerdau", "milhoes", "retorno"} {
		if !got[want] {
			t.Errorf("ExpectedWords did not walk into nested table rows for %q, got %v", want, got)
		}
	}
}

func TestExpectedWordsWalksIntoNestedMapFields(t *testing.T) {
	// A fact-sheet-style layout's lead field is itself a mapping
	// ({label, value}), not a flat string or a list of strings — the branch
	// walkValue's map[string]any case exists for.
	s := deck.Slide{
		Fields: map[string]any{
			"lead": map[string]any{
				"label": "Nome do Projeto",
				"value": "Título do Deck de Exemplo",
			},
		},
	}
	got := words(t, s)
	// Words of at least parity.minSignificantWordLen letters, from both the
	// label and the value: "do" and "de" are too short to carry signal.
	for _, want := range []string{"nome", "projeto", "titulo", "deck", "exemplo"} {
		if !got[want] {
			t.Errorf("ExpectedWords did not walk into the nested lead mapping for %q, got %v", want, got)
		}
	}
}
