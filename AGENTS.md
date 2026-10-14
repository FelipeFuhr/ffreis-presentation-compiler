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

```text
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
| --- | --- | --- |
| HTML | — | pure Go (`html/template`); always produced |
| PDF | `google-chrome`/`chromium` | `--headless=new --print-to-pdf` |
| PNG | Chrome over CDP | one image/slide in `slides/`, at 2x CSS resolution |
| PPTX | Chrome over CDP | image mode: full-bleed render; text as notes |

**Page geometry is the load-bearing detail.** `internal/render/base.gohtml`
emits `@page { size: <W>in <H>in; margin: 0 }` and gives `.slide` exactly those
dimensions with `break-after: page`. That is what makes one slide equal one PDF
page; a theme that overrode those rules would break pagination, which is why the
document shell lives in the compiler and not in the theme. `make smoke-check`
asserts PDF page count equals slide count and that the page size is unchanged.

## Generator hook (charts and diagrams)

A slide can declare `chart:` (or its alias `diagram:`) instead of a `media:`
path. The compiler runs the generator before rendering and writes the result to
the slide's `media` field, so layouts cannot tell a generated image from an
authored one:

```yaml
chart:
  generator: python        # python | cmd | mermaid
  spec: charts/bars.py
  data: data/throughput.csv
  args: ["--title", "Units per week"]
```

The contract a generator honours:

```text
<tool> <spec> --out <path> --tokens <json> [--data <path>] [args…]
```

**`--tokens` is the point of the hook.** It carries the theme's `:root` custom
properties, so a chart is the deck's accent colour by construction rather than
because someone remembered to match it. The same `bars.py` renders blue in the
sample theme and cyan in a brand theme, with no edit.

Output is content-addressed under `<out>/generated/<hash>.svg`; the key covers
the spec, the data **and** the tokens, so changing the theme regenerates charts
rather than serving a stale one. A generator that exits 0 without writing a
non-empty file is treated as a failure, not a no-op.

`mermaid` is registered but does not receive `--tokens` — `mmdc` themes through
its own config, so diagrams inherit the brand less automatically than python
charts do.

## PPTX export

`-formats pptx` writes a PowerPoint file in **image mode**: each slide is the
render, and the slide's kicker, title and `notes` are preserved as speaker
notes. Fidelity is exact; nothing is editable. The deck source stays the
editable artefact — this is for handing a file to someone who expects `.pptx`.

**The non-obvious part is the zip, not the XML.** `archive/zip`'s ordinary
`Create`/`CreateHeader` path streams each entry and appends a *data descriptor*,
setting general-purpose bit 3 and leaving CRC and sizes zero in the local
header. That is valid ZIP, and python-pptx reads such a file without complaint —
but LibreOffice refuses the whole document with nothing but "source file could
not be loaded". `internal/engine/pptx` therefore compresses each part up front
and writes through `CreateRaw` with CRC and sizes populated. `pptx_test.go`
asserts bit 3 is clear on every local header, because the failure mode gives you
no clue where to look.

Two smaller requirements learned the same way: `[Content_Types].xml` must be the
first entry, and `FileHeader.Modified` must be set — the zero value writes month
0, day 0, which is not a real date. A fixed epoch also makes the output
reproducible.

`make export-selftest` round-trips the generated file through LibreOffice and
fails if it will not open, which is the only check that would have caught any of
this.

## Checks (`check`)

`check` measures a **rendered** deck over CDP — it reads `dist/index.html`
rather than re-rendering, so what ships is what gets measured. Rules:
`overflow`, `out-of-bounds`, `tiny-type`, `contrast` (WCAG AA), `missing-asset`,
`font-fallback`.

These are measurements, not opinions. On its first run against a real deck it
found three clipping bugs in slides that had already been eyeballed and passed,
two of them significant (32px and 60px of content silently cut).

- **`document.fonts.check()` is the obvious API and the wrong one.** It answers
  "can this text be rendered", which is *true* for an unknown family because the
  fallback can render it — so a rule built on it never fires. The probe inspects
  the `document.fonts` FontFace set instead and skips families with no
  `@font-face`, which are system fonts the theme chose deliberately.
- **`-overflow-tolerance-px` defaults to 2.5.** Themes are authored in inches,
  so almost every box has a fractional pixel height and a stack of them
  accumulates 1–3px nobody can see. Raise it only with evidence, never to make a
  deck pass.
- `make check-selftest` renders `examples/check-fixture` — a deliberately broken
  deck and theme — and fails unless **all six rules fire**. Without it a checker
  that silently stopped working would look exactly like a clean deck.

## Contract (`validate`)

`validate` checks a **loaded** deck against its theme's field vocabulary
before anything renders — `deck.Load` itself only fails on a missing
`layout:` key, so without this stage a bad field reaches a Go template and
produces either a blank region or a confusing template error three steps
downstream. `build` runs the same check before generators and render, and
aborts on findings; there is no flag to skip it. Both report every finding
in one pass, naming the slide's source file and the offending key, not just
the first problem.

A theme's field vocabulary is declarative, the same idiom as `.Fields`
itself: `internal/contract/contract.yaml` lists, per layout, the keys it
`required`s and the keys it additionally `allowed`s; any other front-matter
key is an error. That file is `//go:embed`ded as the compiler's shipped
default, describing the sample theme's layouts. A theme overrides it
wholesale with its own `contract.yaml` at its theme root — adding a layout is
then a YAML change, not a Go change. A `chart:`/`diagram:` block is
recognised on every layout unconditionally, the same way chrome keys are, so
it is never part of a layout's own vocabulary.

Rules: `unknown-layout` (names an available layout, from
`theme.LayoutNames()`), `missing-field`, `unknown-field`, `missing-asset`
(a `media`, or a chart/diagram `spec`/`data`, that does not exist on disk),
`unsafe-path`, `invalid-field` (a malformed chart/diagram block, or an asset
field that is not a non-empty string), `invalid-contract` (a theme's own
`contract.yaml` fails to parse). Deliberately not implemented: a
duplicate-slide-id check. Two slides sharing a title and layout is not an
error — a consumer deck legitimately repeats slides — and detecting a
duplicate by source basename alone could not be made precise without risking
false positives, so the rule was skipped rather than shipped noisy.

**Path containment is the security-sensitive part.** Three places resolve a
deck- or theme-author-supplied path against a root: `theme.Load` joins
`deck.yaml`'s `theme:` value onto the themes root; `gen.Registry.Resolve`
joins a chart/diagram block's `spec`/`data` onto the deck root; `validate`
itself joins a slide's `media` (and, redundantly, the same chart/diagram
`spec`/`data`) onto the deck root to check it exists. `filepath.Join` alone
does not stop a `..` traversal, and a naive `strings.HasPrefix` check on the
joined absolute path is its own trap — root `/a/deck` has the same string
prefix as the unrelated sibling `/a/deckx/f`. `internal/pathsafe.Contain` is
the one helper all three call: it resolves via `filepath.Rel` and rejects an
absolute candidate outright, so every call site gets the same answer to "does
this still live under root". A theme name additionally may not contain a
path separator at all — it names a single subdirectory, never a path — which
`theme.Load` rejects before the join is even attempted.

`make validate-selftest` renders `examples/contract-fixture` — a
deliberately broken deck against the sample theme — and fails unless **all
five rules fire**. Without it a validator that silently stopped catching one
of them would look exactly like a clean deck.

## Commands

```bash
go run ./cmd/ffreis-presentation-compiler build \
  -deck-root examples/sample-deck -theme-root examples/themes \
  -out dist -formats html,pdf
go run ./cmd/ffreis-presentation-compiler validate \
  -deck-root examples/sample-deck -theme-root examples/themes
go run ./cmd/ffreis-presentation-compiler doctor

make build             # render the example deck (override DECK= THEMES= OUT= FORMATS=)
make validate-deck     # check the example deck's contract (override DECK= THEMES=)
make validate-selftest # prove the validator still catches all five rules
make smoke-check       # end-to-end render + pagination assertion
make check             # render the example deck and measure it
make check-selftest    # prove the checker still catches all six rules
make export-selftest   # prove the generated pptx actually opens
make doctor            # which adapters are available here
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

```text
cmd/ffreis-presentation-compiler/  one-liner → cli.Run
internal/cli/                      dispatch + per-subcommand flag sets
internal/deck/                     content model and loader
internal/theme/                    theme loading, template FuncMap
internal/contract/                 field-vocabulary validation (contract.yaml)
internal/pathsafe/                 the one path-containment helper, shared by theme/gen/contract
internal/render/                   document shell + slide assembly (base.gohtml)
internal/check/                    measured checks (probe.js)
internal/engine/                   Renderer port, Runner, PDF/PNG/PPTX adapters
internal/engine/chrome/            CDP client, shared by check + adapters
internal/engine/pptx/              OOXML writer (assets/ holds the static parts)
internal/gen/                      generator hook (charts, diagrams) + cache
internal/logx/                     slog construction
examples/sample-deck/              brand-free fixture deck
examples/themes/sample/            brand-free fixture theme
examples/check-fixture/            deliberately broken deck, for check-selftest
examples/themes/check-fixture/     deliberately broken theme, ditto
examples/contract-fixture/         deliberately broken deck, for validate-selftest
```

## Roadmap

Implemented: content model, theme + layouts, HTML, PDF, PNG, PPTX (image mode),
the generator hook, `check`, `validate`, `doctor`, and four self-tests. Next,
in order: an `overlap` rule (deferred — parent/child and absolutely-positioned
chrome make it the most false-positive-prone of the set); `serve --watch`;
model-assisted review.

## Keeping this file current

- Add facts you had to discover the hard way; that is what this file is for.
- Correct anything here that turns out to be wrong — stale guidance is worse
  than none.
- Update the structure tree when packages are added or renamed.
