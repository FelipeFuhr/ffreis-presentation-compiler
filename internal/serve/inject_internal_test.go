package serve

// These tests exercise unexported injection helpers directly, so this file
// lives in package serve rather than serve_test.

import (
	"bytes"
	"strings"
	"testing"
)

func TestInjectReloadAddsScriptBeforeClosingBody(t *testing.T) {
	doc := []byte("<html><body><h1>hi</h1></body></html>")
	out := injectReload(doc)

	if !strings.Contains(string(out), "EventSource(\"/__reload\")") {
		t.Fatalf("injected output does not open the reload stream:\n%s", out)
	}
	if strings.Index(string(out), "<script>") > strings.Index(string(out), "</body>") {
		t.Fatalf("script was not inserted before </body>:\n%s", out)
	}
}

func TestInjectReloadDoesNotMutateTheInputSlice(t *testing.T) {
	original := []byte("<html><body>content</body></html>")
	// Keep an independent copy to compare against: a correct implementation
	// never writes through the slice it was handed, since that slice is the
	// exact bytes render.Render wrote to disk.
	untouched := append([]byte{}, original...)

	injectReload(original)

	if !bytes.Equal(original, untouched) {
		t.Fatalf("injectReload mutated its input: got %q, want %q", original, untouched)
	}
}

func TestInjectReloadAppendsWhenThereIsNoClosingBodyTag(t *testing.T) {
	doc := []byte("<html><body>no closing tag here")
	out := injectReload(doc)
	if !strings.HasPrefix(string(out), string(doc)) {
		t.Fatalf("output does not preserve the original document:\n%s", out)
	}
	if !strings.Contains(string(out), "location.reload()") {
		t.Fatalf("output does not carry the reload script:\n%s", out)
	}
}

func TestInjectErrorBannerKeepsTheLastGoodContentVisible(t *testing.T) {
	doc := []byte("<html><body><h1>the deck</h1></body></html>")
	out := injectErrorBanner(doc, "slide 2: front-matter is missing required key \"layout\"")

	text := string(out)
	if !strings.Contains(text, "<h1>the deck</h1>") {
		t.Fatalf("last-good content was lost:\n%s", text)
	}
	if !strings.Contains(text, "rebuild failed") || !strings.Contains(text, "missing required key") {
		t.Fatalf("error is not visible in the banner:\n%s", text)
	}
}

func TestInjectErrorBannerEscapesTheMessage(t *testing.T) {
	doc := []byte("<html><body>ok</body></html>")
	out := injectErrorBanner(doc, "<script>alert(1)</script>")
	if strings.Contains(string(out), "<script>alert(1)</script>") {
		t.Fatalf("error message was not escaped, breaking out of the banner:\n%s", out)
	}
}

func TestStandaloneErrorPageIsServedWhenThereIsNoLastGoodRender(t *testing.T) {
	out := standaloneErrorPage("deck.yaml: parse error")
	text := string(out)
	if !strings.Contains(text, "deck.yaml: parse error") {
		t.Fatalf("error is not visible:\n%s", text)
	}
	if !strings.Contains(text, "EventSource") {
		t.Fatalf("standalone error page does not reload once the deck is fixed:\n%s", text)
	}
}
