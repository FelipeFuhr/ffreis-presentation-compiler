package serve

import "net/http"

// routes assembles the server's HTTP handler: "/" and "/index.html" serve
// the in-memory, reload-injected page; "/__reload" is the SSE endpoint the
// injected script connects to; everything else falls through to the files
// the last render wrote (assets, fonts, generated charts).
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/__reload", s.broker.serveSSE)
	mux.HandleFunc("/", s.serveIndexOrFile)
	return mux
}

func (s *Server) serveIndexOrFile(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" || r.URL.Path == "/index.html" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(s.site.page())
		return
	}
	http.FileServer(http.Dir(s.outDir)).ServeHTTP(w, r)
}
