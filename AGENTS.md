# Agent Context

**This repo:** `ffreis-presentation-compiler` — Go CLI that compiles declarative
slide decks (YAML front-matter + Markdown + a brand theme) to HTML, PDF and PNG.
It is the slide-deck analog of `ffreis-latex-compiler` and follows the same
conventions (handwritten CLI dispatch on `os.Args[1]`, `log/slog` via
`internal/logx`, ports-and-adapters over the external render toolchain).

## Public repo — private-repo hygiene

This is a **public** GitHub repository. When writing commit messages, PR titles,
PR descriptions, code comments, or any other user-visible text, **never name
private repos** — the deck-content repos that consume this tool, their brands,
or any client material. Use generic terms: "a consumer repo", "a brand theme",
"the deck repo". The compiler core is deliberately generic: it takes
`-deck-root` and `-theme-root` as flags and hardcodes no consumer, which is
exactly what lets it stay public.

The shipped `examples/` deck and theme are brand-free for the same reason. Do
not add a real brand's palette, logo or content to them.

## Inputs and outputs

Consumer-agnostic. It expects:

- a **deck** root: `deck.yaml` + `slides/*.md` (+ optional `assets/`);
- a **themes** root containing one subdirectory per theme.

```
<deck-root>/                      <themes-root>/<name>/
  deck.yaml                         theme.yaml
  slides/NN-name.md                 css/*.css        (concatenated in filename order)
  assets/                           slide.gohtml     (chrome wrapper)
                                    layouts/*.gohtml (one per layout)
                                    assets/ fonts/
```

Slide files are YAML front-matter plus a Markdown body. Chrome keys (`layout`,
`kicker`, `title`, `phase`, `badge`, `notes`, `skip`) are typed; **every other
key is layout vocabulary** and reaches templates as `.Fields`, so adding a
layout needs a template, not a Go change. Deck order is filename order — hence
the `NN-` prefix convention. `skip: true` drops a slide and renumbers the rest.

Outputs land in `-out` (default `dist/`): `index.html` plus copied
`assets/`/`fonts/`, then one artifact per requested format.

## Toolchain (ports + adapters, `internal/engine`)

HTML is produced in-process; every format derived from it sits behind a
`Renderer`, and adapters shell out via an injectable `Runner` so command
construction is unit-tested with no browser installed.

| Format | Tool | Notes |
|---|---|---|
| HTML | — | pure Go (`html/template`); always produced |
| PDF | `google-chrome` (or `chromium`) | `--headless=new --print-to-pdf`; one slide per page |
| PNG | — | not implemented yet; `-formats png` fails loudly rather than silently skipping |

**Page geometry is the load-bearing detail.** `internal/render/base.gohtml`
emits `@page { size: <W>in <H>in; margin: 0 }` and gives `.slide` exactly those
dimensions with `break-after: page`. That is what makes one slide equal one PDF
page; a theme that overrode those rules would break pagination, which is why the
document shell lives in the compiler and not in the theme. `make smoke-check`
asserts PDF page count equals slide count and that the page size is unchanged.

## Commands

```bash
go run ./cmd/ffreis-presentation-compiler build \
  -deck-root examples/sample-deck -theme-root examples/themes \
  -out dist -formats html,pdf
go run ./cmd/ffreis-presentation-compiler doctor

make build        # render the example deck (override DECK= THEMES= OUT= FORMATS=)
make smoke-check  # end-to-end render + pagination assertion
make doctor       # which adapters are available here
```

## Non-obvious facts

- **`COVERAGE_MIN` is 85, not the fleet-default 90.** The uncovered remainder is
  `cli.Run` (an `os.Exit` wrapper) and the adapter success paths, which only
  execute with a browser present. Those are covered by `make smoke-check`, which
  actually renders a PDF, rather than by a unit test that would silently pass on
  a machine with no Chrome. Same reasoning `ffreis-resume` documents.
- **Tests must not depend on a browser being installed.** An early version of
  `engine_test.go` asserted the default `Runner` would fail; it passed on a
  machine with Chrome and failed everywhere else. Stub `engine.LookPath` and
  inject a `Runner` instead.
- **Reading `shape.line.color`-style accessors is not a concern here, but the
  equivalent trap is `template.HTML`/`template.CSS` conversions** — they are
  deliberate and each carries a `//nolint:gosec` naming the trust boundary
  (theme-authored source, never deck content or external input).
- `internal/engine` is deliberately **slide-agnostic** — it knows pages, sizes
  and files, never decks, themes or layouts. That is what would let it become
  the fleet's shared `html-render` capability, with `ffreis-resume` dropping its
  own `scripts/pdf.sh` in favour of it.
- Chrome flags follow `ffreis-resume/scripts/pdf.sh`: `--no-sandbox` and
  `--disable-dev-shm-usage` are what make the PDF path work inside CI containers
  and `act`.

## Structure

```
cmd/ffreis-presentation-compiler/  one-liner → cli.Run
internal/cli/                      dispatch + per-subcommand flag sets
internal/deck/                     content model and loader
internal/theme/                    theme loading, template FuncMap
internal/render/                   document shell + slide assembly (base.gohtml)
internal/engine/                   Renderer port, Runner, PDF adapter
internal/logx/                     slog construction
examples/sample-deck/              brand-free fixture deck
examples/themes/sample/            brand-free fixture theme
```

## Roadmap

Implemented: content model, contract-free loading, theme + layouts, HTML, PDF,
`doctor`, smoke check. Next, in order: remaining layouts and an explicit
contract file; the generator hook (charts and diagrams rendered from specs with
theme tokens injected); `check` — deterministic validators over the live DOM
(overflow, out-of-bounds, overlap, contrast, fonts); `serve --watch`; PPTX in
image mode; model-assisted review.

## Keeping this file current

- Add facts you had to discover the hard way; that is what this file is for.
- Correct anything here that turns out to be wrong — stale guidance is worse
  than none.
- Update the structure tree when packages are added or renamed.
