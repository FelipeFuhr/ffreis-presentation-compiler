module github.com/FelipeFuhr/ffreis-presentation-compiler

go 1.25

// govulncheck (a hard pre-push gate via `make quality-gates`) reports CVEs in
// the standard library itself, fixed only by moving the toolchain forward —
// most recently GO-2026-6809 (crypto/x509), fixed at exactly go1.26.10.
// Without this the repo cannot be pushed at all. Go's toolchain manager
// fetches this on demand, so no SDK install is needed; the workflows'
// go-version is raised to match so CI does not silently build on a different
// toolchain than it reports.
toolchain go1.26.10

require (
	github.com/chromedp/cdproto v0.0.0-20250724212937-08a3db8b4327
	github.com/chromedp/chromedp v0.14.2
	github.com/yuin/goldmark v1.8.6
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/chromedp/sysutil v1.1.0 // indirect
	github.com/go-json-experiment/json v0.0.0-20260213210345-44df1a37e875 // indirect
	github.com/gobwas/httphead v0.1.0 // indirect
	github.com/gobwas/pool v0.2.1 // indirect
	github.com/gobwas/ws v1.4.0 // indirect
	golang.org/x/sys v0.34.0 // indirect
)
