package serve

import (
	"bytes"
	"fmt"
	htmlpkg "html"
)

// reloadScript is appended to the served HTML only — never to what `build`
// writes. It opens one Server-Sent Events connection to /__reload and
// reloads the page on every event; a dropped connection (the server
// restarting mid-rebuild) retries after a short delay instead of giving up.
const reloadScript = `<script>
(() => {
  const es = new EventSource("/__reload");
  es.onmessage = () => location.reload();
  es.onerror = () => {
    es.close();
    setTimeout(() => location.reload(), 1000);
  };
})();
</script>`

// errorBannerTemplate overlays a fixed banner naming the rebuild error. %s is
// the escaped error text.
const errorBannerTemplate = `<div style="position:fixed;top:0;left:0;right:0;z-index:2147483647;` +
	`background:#b91c1c;color:#fff;padding:12px 16px;` +
	`font:14px/1.4 -apple-system,Segoe UI,sans-serif;white-space:pre-wrap;">` +
	`&#9888; rebuild failed: %s</div>`

// injectReload inserts reloadScript just before the closing </body> tag, or
// appends it if the document has none. doc is never modified in place: the
// caller holds the bytes render.Render wrote to disk, and those must stay
// byte-for-byte what `build` would have produced.
func injectReload(doc []byte) []byte {
	idx := lastIndexFold(doc, []byte("</body>"))
	if idx < 0 {
		return append(append([]byte{}, doc...), []byte(reloadScript)...)
	}
	out := make([]byte, 0, len(doc)+len(reloadScript))
	out = append(out, doc[:idx]...)
	out = append(out, []byte(reloadScript)...)
	out = append(out, doc[idx:]...)
	return out
}

// injectErrorBanner overlays an error banner onto the last good render, right
// after the opening <body> tag, so the author still sees the deck while being
// told the latest edit broke the build.
func injectErrorBanner(doc []byte, msg string) []byte {
	banner := []byte(fmt.Sprintf(errorBannerTemplate, htmlpkg.EscapeString(msg)))
	idx := indexFold(doc, []byte("<body"))
	if idx < 0 {
		return append(append([]byte{}, banner...), doc...)
	}
	end := bytes.IndexByte(doc[idx:], '>')
	if end < 0 {
		return append(append([]byte{}, banner...), doc...)
	}
	insertAt := idx + end + 1
	out := make([]byte, 0, len(doc)+len(banner))
	out = append(out, doc[:insertAt]...)
	out = append(out, banner...)
	out = append(out, doc[insertAt:]...)
	return out
}

// standaloneErrorPage is served when the very first render has failed, so
// there is no last-good HTML to overlay a banner onto.
func standaloneErrorPage(msg string) []byte {
	banner := fmt.Sprintf(errorBannerTemplate, htmlpkg.EscapeString(msg))
	page := fmt.Sprintf(
		`<!doctype html><html><head><meta charset="utf-8"><title>rebuild failed</title></head>`+
			`<body>%s</body></html>`,
		banner,
	)
	return append([]byte(page), []byte(reloadScript)...)
}

func lastIndexFold(doc, sub []byte) int {
	return bytes.LastIndex(bytes.ToLower(doc), bytes.ToLower(sub))
}

func indexFold(doc, sub []byte) int {
	return bytes.Index(bytes.ToLower(doc), bytes.ToLower(sub))
}
