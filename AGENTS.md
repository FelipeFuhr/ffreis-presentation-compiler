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

**The shell also declares the rem basis, and themes author purely in rem.**
`:root { font-size: calc(var(--slide-height) / 45) }` makes 1rem exactly 1/45
of the slide height — 16px at the 7.5in height both supported aspects share —
so a slide is 45rem tall and, at 16:9, 80rem wide. That is what lets a theme
reason about the body box at all, and it is the single knob that rescales a
whole deck.

- **Height, not width.** 4:3 is narrower at the same height, so deriving the
  basis from the width would silently shrink a 4:3 deck's body text by a
  quarter.
- **Deleting the declaration breaks nothing visibly.** The browser falls back
  to its own 16px default, which is the same number at 7.5in — so the deck
  renders correctly today and keeps absolute type the day a deck is paged at
  any other size. `TestRenderDeclaresTheRemBasis` is what catches that.
- **Page geometry stays in inches**, deliberately: `@page` takes a physical
  paper size, and expressing the paper in units derived from the paper would be
  circular.
- Themes should put spacing on a 0.25rem (4px) grid. The first theme was
  authored in inches, which gave nearly every box a fractional pixel height
  (0.14in = 13.44px); a stack of them accumulated the rounding that
  `-overflow-tolerance-px` exists to absorb.

## Template functions (`internal/theme`)

Layouts get a small FuncMap: `get`, `str`, `int`, `list`, `rows`, `add`,
`default` and `seq`.

`seq lo hi` returns the inclusive integer range, so a layout can lay out a
fixed grid — a 12-month Gantt, a rating scale — without the deck repeating
the column list in YAML on every row. It returns **nil** for an inverted or
oversized range (`MaxSeq`, 1000) rather than erroring: a grid that renders no
cells is obvious on the slide, where aborting the build hides which layout
asked for it.

A layout indexing into an optional span must guard it. `index` on an empty
slice aborts the render, so the Gantt layout defaults its bounds to `0`/`-1`
and only reads the span when it has two entries — an activity with no dates
yet then renders empty tracks instead of failing the build.

## Generator hook (charts and diagrams)

A slide can declare `chart:` (or its alias `diagram:`) instead of a `media:`
path. The compiler runs the generator before rendering and writes the result to
the slide's `media` field, so layouts cannot tell a generated image from an
authored one:

```yaml
chart:
  generator: python        # python | cmd | mermaid | d2
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
legitimate artifact is treated as a failure, not a no-op — and not cached as
one either: `Resolve` deletes whatever it wrote before returning the error, so
a broken artifact never survives at its own content-hash path to be served to
the next build with identical inputs.

**"Legitimate" is `gen.validateArtifact`, two checks deep.** First, size:
`gen.MinArtifactBytes` (256) rejects anything smaller, which an `<svg/>` root
with no children — a generator that crashed before drawing anything but still
exited 0 — cannot clear; a real chart or diagram runs into the thousands of
bytes. Second, format: for `svg`, the bytes must contain both `<svg` and
`</svg>`; for `png`, they must start with the 8-byte PNG signature. Both
checks are deliberately shallow — this is catching a crashed or
misconfigured generator, not judging a trusted tool's output byte for byte,
which is what `parity` (below) exists for instead.

`mermaid` is registered but does not receive `--tokens` — `mmdc` themes through
its own config, so diagrams inherit the brand less automatically than python
charts do.

`d2` takes no `--tokens` argument either, for a different reason: d2 has no
runtime flag for custom colours, only a `vars: { d2-config: { theme-overrides:
{ … } } }` block in the diagram's own source. So for this one generator the
compiler rewrites a copy of the spec before invoking it, prepending that block
from whichever of a small, documented token subset (`accent`, `accent-text`,
`paper`, `paper-strong` → D2's `B1`, `N1`, `N7`, `N6` slots) is present and a
plain hex colour — d2 rejects `rgba()` and other CSS functions outright, so a
token in that shape is dropped rather than forwarded to fail. A theme's own
vars still apply to anything that subset does not cover. `d2`'s command line
is positional (`d2 <spec> <out>`), not the `--out`/`--tokens` flag contract the
other generators share — that part is d2's own CLI, unrelated to the rewrite.

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

**`CreateRaw` writes exactly the header you hand it, and that is the whole
hazard.** Every default `CreateHeader` derives on your behalf is simply absent.
Three of them have now bitten this package, each found only after a file had
already shipped:

- **general-purpose bit 3 / CRC / sizes** — the streaming path's data
  descriptor, above;
- **`Modified`** — the zero value writes month 0, day 0, which is not a real
  date (a fixed epoch also makes the output reproducible);
- **`ReaderVersion` / `CreatorVersion`** — `CreateHeader` sets these
  unconditionally (`fh.ReaderVersion = zipVersion20`, and the low byte of
  `CreatorVersion` likewise). `CreateRaw` sets neither, so every entry declared
  "version needed to extract 0.0" — not a lenient value, not an old value, but
  the Go zero value, which is not a ZIP version at all. Deflate requires 2.0.

So before adding any field here, ask what `CreateHeader` would have derived
from it. The constants (`zipEpoch`, `zipModDate`, `zipModTime`, `zipVersion`)
exist to make that derivation explicit rather than implied.

**The version-field bug is the one worth internalising, because every tool to
hand called the file valid.** `zipfile.testzip()`, `unzip -t`, python-pptx,
LibreOffice and current PowerPoint all ignore the declared version — it is
advisory, and they infer capability from the data instead. An older desktop
Office install does not: it validated the field, found 0.0, and rejected the
*entire package* with a generic "there was a problem with the content / repair?"
prompt that names no part and no reason. A reader that is stricter than your
test suite is indistinguishable from data corruption.

`pptx_test.go` therefore asserts these fields at the **byte** level, parsing the
local and central-directory headers directly rather than through `archive/zip`:
a library that round-trips its own defaults cannot catch a default it never
applied. `TestHeadersDeclareARealZipVersion` is that assertion.

`make export-selftest` round-trips the generated file through LibreOffice, which
is what caught the data-descriptor bug. **It would not have caught the other
two** — LibreOffice accepts both a zero date and a zero version — and it is
local-only: no workflow runs it, and it `exit 0`s when `soffice` is absent, so
its silence means nothing. Treat it as one lenient reader's opinion, not as
proof the file opens. The reader whose opinion actually matters is the one
below.

## PowerPoint acceptance (`acceptance/powerpoint/`)

The only check here that asks **real desktop Microsoft PowerPoint** whether it
will open what this compiler wrote. It exists because the zip-version bug above
was accepted by every reader this repo could reach — `zipfile`, `unzip -t`,
python-pptx, LibreOffice, current PowerPoint — and refused by the desktop
PowerPoint the file was sent to. There was no reader in the toolchain strict
enough to disagree, so a whole class of packaging defect could ship.

It is Python, in an otherwise-Go repo, because `pywin32` is the only practical
way to drive PowerPoint's COM API. `make pptx-acceptance PROFILE=<name>` runs
it in a disposable Windows VM under QEMU/KVM; `acceptance/powerpoint/README.md`
carries the provisioning steps and the full design.

**It has never been executed against a real PowerPoint.** No licensed Windows
or Office image exists here yet. Nothing in this repo may be called
PowerPoint-verified until one does — that is the entire discipline this harness
is for, so do not soften it.

Four constraints in that code are load-bearing:

- **`OpenAndRepair=msoFalse`, and the dialog is never answered.** Repair
  silently fixes a malformed package and hands back a working presentation, so
  a test that allowed it would pass on exactly the files this exists to catch.
  A dialog that appears anyway is read for its text and reported.
- **The COM calls run in a child process.** A modal dialog parks the calling
  thread inside `Open2007` until a button is pressed, so the process that
  opened the file cannot be the one that notices. `runner.py` owns the deadline
  and polls for dialogs from outside; `ppt_worker.py` writes its PID file
  *before* opening, because after that it may never reach another line.
- **Only our own PowerPoint is terminated**, identified by diffing the running
  `POWERPNT.EXE` set across our own `DispatchEx` — never by image name. The
  harness refuses to start at all while another PowerPoint is open rather than
  risk driving someone's window.
- **A skip is not a pass.** Absent prerequisites skip by default and *fail*
  when `POWERPOINT_ACCEPTANCE_REQUIRED` is set, which the VM orchestrator and
  the dispatched CI job both do. Declared as the `powerpoint_acceptance` flag.

A negative control (`[Content_Types].xml` stripped from the real artifact) must
be rejected, or every pass in the file is void — the same reasoning as
`check-selftest`. And a **diagnostic** rebuilds the original defect (all
version fields back to `0.0`, every part otherwise byte-identical) and records
whether this PowerPoint rejects it, without asserting: the field is advisory,
so the answer describes the installed PowerPoint, not the compiler. Run across
profiles it is what turns "a reader could not open it" into "PowerPoint *x.y*
rejects this field".

No automatic trigger and no default runner label: GitHub's hosted windows
runners ship no Office and have no interactive desktop, and no runner in this
fleet carries a licensed PowerPoint. A job queueing forever against a label
nobody serves looks like a gate while gating nothing.

## SEO (`internal/seo`)

A deck served over HTTP gets the same discovery surface as the fleet's
websites: the same meta vocabulary a consumer site's head partial emits,
`og:*` under `property` and `twitter:*` under `name`, a schema.org block, a
sitemap and a robots policy. Three things differ on purpose.

- **A deck defaults to `noindex, nofollow`.** A website wants to be found; a
  deck is usually unpublished client material that happens to be HTML, so the
  safe default is the opposite one. Setting `seo.base_url` is what declares a
  deck published — and from that point its SEO contract is a **build gate**,
  not advice. Without it, a deck gets a title and a noindex and nothing else,
  and no sitemap or robots.txt is written. There is deliberately no
  `-strict-seo` flag: the deck already answers whether its defects matter.
- **The JSON-LD is marshalled, not interpolated.** Every website in the fleet
  builds its `ld+json` by substituting values into a template literal, so a
  description containing a quote emits a block no parser accepts — silently,
  because nothing validates it. `encoding/json` cannot make that mistake, and
  it escapes `<`/`>` so no value can close the `<script>` early.
- **`@type` is `PresentationDigitalDocument`**, not `WebPage`. It is accurate,
  and `numberOfPages` is a property only a document type carries, so a crawler
  learns the deck's length from the markup.

Also note: `thumbnailUrl` is typed `URL` by schema.org and `image` is
`ImageObject`-or-`URL`. Putting an `ImageObject` under `thumbnailUrl` is a type
error nothing in this fleet would have caught.

`internal/seo` is a **leaf package** — it imports nothing from this module, so
`internal/deck` can hold the `seo:` block. That is why `Head` takes a
`seo.Document` rather than a `*deck.Deck`.

`seo.Validate` adds the bounds no tool in the fleet checks: title ≤ 60 chars,
description 50–160, counted in **runes** (a Portuguese title is shorter than
its byte length). The fleet's own validator asserts presence only, and
html-validate's `long-title` rule is explicitly switched off.

```bash
make seo-selftest   # every required tag present + ld+json parses; no Node needed
make seo-audit      # Lighthouse, SEO category, floor 0.9 — the fleet's gate
```

`seo-selftest` is the one that runs anywhere; it fails naming each missing tag.
`seo-audit` pins Lighthouse rather than tracking latest, because a Lighthouse
minor can move an audit between categories and turn the gate red with no change
to the deck. It **fails loudly** when Node is absent rather than skipping — a
skip that reads as a pass is how a gate stops gating.

## Document language

`-lang` defaults to **empty**, resolving as: the flag, then `deck.yaml`'s
`lang:`, then `deck.DefaultLang` (`"en"`). The language belongs to the content,
not the invocation. The previous default was `pt-BR` — one consumer's language
baked into a public, consumer-agnostic compiler, which is exactly what the
`-deck-root`/`-theme-root` discipline exists to avoid.

**That default left residue in two places, both since removed, and the shape
of the leak is worth recognising.** `serve`'s own `-lang` still defaulted to
`pt-BR`; because `render.Render` resolves its `Options.Lang` through
`deck.Language`, a non-empty default there *overrode* every deck's own `lang:`
on every preview — the one case where the flag wins. And the PPTX
speaker-notes writer stamped `lang="pt-BR"` on every run of text. Both now
take the resolved value: `build` puts it on `engine.Job.Lang` (a property of
the document being exported, like `Size`), and `pptx` falls back to
`pptx.DefaultNotesLang` when a deck names none, because the zero value would
write `lang=""` into the XML. The notes tag only sets PowerPoint's
spell-check language for the notes pane, which is exactly why it survived
unnoticed: a leak with no visible symptom is the kind a test has to hold.

## Checks (`check`)

`check` measures a **rendered** deck over CDP — it reads `dist/index.html`
rather than re-rendering, so what ships is what gets measured. Rules:
`overflow`, `out-of-bounds`, `tiny-type`, `contrast` (WCAG AA), `missing-asset`,
`font-fallback`, `overlap`, `pdf-pages`.

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
- **`overlap` is a pairwise area check, not a class list.** It was the one rule
  deferred for being the most false-positive-prone of the set (see Roadmap
  history), so the policy it landed with is worth being explicit about:
  - Only **leaf text blocks** are compared — elements whose own `ownText` is
    non-empty, the same primitive `tiny-type`/`contrast` already use. A
    container is never blamed for its children.
  - Ancestor/descendant pairs are skipped via `.contains()` — they always
    intersect by construction, so comparing them would be pure noise.
  - The policy for chrome that sits over the body **by design** (the slide
    badge, the phase pill) is structural, not a hardcoded class list:
    elements are compared only when both are in **normal flow**
    (`position: static` or `relative`). Anything absolutely or
    fixed-positioned is excluded outright. `data-overlap-ignore` is the
    theme-declared escape hatch for anything else that legitimately overlaps
    — the same pattern as `data-measure` / `data-slide` — set it on the
    element, not a selector list in Go.
  - A **real area of overlap** is required
    (`-overlap-min-area-px`, default `64`, see `check.DefaultOverlapMinAreaPx`)
    so two inline siblings sharing a line do not fire on a touching edge or a
    sub-pixel rounding sliver.
  - The comparison is `O(n^2)` over one slide's text leaves — intentionally;
    that is fine at slide scale, so do not "optimise" it into something that
    skips a real collision. A slide with an implausible number of leaves
    (`MAX_OVERLAP_LEAVES` in `probe.js`) skips the comparison outright instead
    of hanging the check.
- **`pdf-pages` is Go, not DOM.** A silently dropped or doubled PDF page is
  invisible until someone presents, and PDF page count is not something CDP
  measures. `check -pdf <file>` counts pages by scanning for `/Type /Page`
  object dictionaries directly in the PDF bytes — deliberately not shelling
  out to `pdfinfo`, so the check still works where poppler is not installed.
  **The trap is `/Type /Pages`** (the page-tree node): a naive scan without a
  trailing word boundary overcounts by at least one on every real PDF.
  `build` runs this comparison automatically whenever `-formats` produced a
  PDF, so the common path is covered without anyone remembering the flag.
- `make check-selftest` renders `examples/check-fixture` — a deliberately broken
  deck and theme — and fails unless **all eight rules fire**. Without it a checker
  that silently stopped working would look exactly like a clean deck.

## Golden images (`golden`, `internal/golden/`)

Visual regression for the shipped example deck. Nothing else in this fleet
owns visual regression today, so this is new, deliberately minimal ground:
render `examples/sample-deck` to PNG (one image per slide, at 2x — the PNG
adapter `internal/engine` already ships) and compare each slide against an
approved baseline under `examples/golden/`.

> **The baselines are NOT committed yet, so `make golden` errors until you
> run `make golden-update` once.** It fails loudly rather than passing
> vacuously, and nothing gates on it, so this is a seeded-capability gap
> rather than a broken build. The blocker is the shared
> `hygiene-binary-files` pre-commit hook, which rejects every binary with no
> allowlist (`platform/ffreis-platform-standards/lefthook/scripts/check_binary_files.sh`),
> and this repo has never committed a binary file. Until that hook grows an
> opt-in allowlist **upstream**, visual regression here is local-only — the
> code is complete and tested, but it cannot catch drift for anyone who has
> not seeded their own baseline, which is most of the point. Do not widen the
> hook from a consumer repo.

- **The comparison is perceptual, not byte-exact.** Chrome's PNG encoder is
  not byte-reproducible across versions — subpixel rounding and anti-aliasing
  shift by a few intensity levels per channel on every release — so a byte
  compare would fail on every Chrome update and teach everyone to re-approve
  the baseline without looking. `internal/golden` decodes both images and
  counts pixels whose combined channel difference exceeds
  `golden.PixelDiffThreshold`; a slide fails when that count, as a fraction of
  its total pixels, exceeds `golden.DefaultDiffRatioTolerance` (1%). Only the
  standard library (`image`, `image/png`) is used — no new dependency for a
  diff this simple.
- **A dimension mismatch is a hard failure, not a 100% diff.** Two
  different-shaped images have no pixel ratio to compute, so `golden` reports
  the slide and both sizes directly (`SlideResult.DimensionsMatch`) instead of
  fabricating a number.
- **`make golden` and `make golden-update` are deliberately separate, and
  `golden-update` is a plain `cp` in the Makefile, not a Go code path at
  all.** The `golden` CLI subcommand has no flag that can write to
  `-baseline`; nothing short of running `make golden-update` by name can ever
  re-approve it, so a typo in a compare invocation can never turn into a
  silent re-approval.
- **A failing slide writes a highlight artifact under `.bin/golden/`**
  (`<slide>.diff.png`: differing pixels in red, everything else dimmed to
  grey) and names the path in its own output line, so a human has somewhere
  to look without being told a bespoke path.
- Baselines are binary files in a Go repo, so they are kept as small as
  reasonably possible for a 7-slide deck (~670 KB total as of this writing).

## OCR content parity (`parity`, `internal/parity/`)

Checks something golden images and `check` cannot: whether the words on a
rendered slide are the words its own source declares. A table can fit the
slide, pass every contrast and overflow rule, and still be wrong — a dropped
section, a hedge like "(a confirmar)" quietly turned into a bare figure, a
whole paragraph a hand port forgot. `check` measures geometry and `golden`
measures pixels; neither has an opinion on content. `parity` OCRs each
rendered slide PNG (`tesseract stdin stdout -l <lang>` — no temp files) and
compares the recognised text against `parity.ExpectedWords(slide)`: the
slide's kicker/title/phase, its rendered body prose, and every string leaf in
its layout fields, walked recursively the same shape `internal/theme`'s
template funcs coerce YAML into.

- **Excluded on purpose:** `Notes` (speaker notes, never painted on the
  slide) and `Badge` (a path) from chrome; `alt`, `media`, `chart`, `diagram`
  from a slide's fields — an asset path or a generator's own config block is
  not prose, and a chart's `args` cannot be told apart from flags
  positionally, so the whole block is out of scope rather than guessed at.
- **Comparison is fuzzy by design.** Both sides are lowercased and folded
  through a small, hand-rolled diacritic table (no new dependency: the
  target is Romance/Germanic Latin-script decks, and a script family that
  table does not cover would not tokenize into words by whitespace-splitting
  anyway), then tokenized to words of at least 4 letters —
  `parity.minSignificantWordLen` — since OCR mangles short, common words the
  most and they carry the least signal.
- **`parity.DefaultMissingWordTolerance` (12%) is deliberately coarse.** It
  exists to catch a dropped section — many of a slide's own words missing at
  once — not a single short phrase on an otherwise text-rich slide. That
  smaller class is why `Report` prints every slide's full missing-word list
  even when it passes tolerance: a human (or a model) reads it the same way
  an adversarial pass already does by hand.
- **OCR language is resolved, not guessed from content.** `-lang` overrides;
  absent that, `parity.OCRLang` maps the deck's own `lang:` (BCP 47) primary
  subtag to a tesseract code, falling back to `eng`. The wrong language model
  does not error — it degrades (accents misread) — which is the right
  failure mode for a check that is already approximate.
- **Advisory, like `review`:** needs an external tool (`doctor` reports it,
  same as the generator hook's python/mermaid/d2), and a missing-word count
  is a prompt to look, not proof of a defect — OCR of real deck PNG files scored
  0/N missing across the entire shipped example on first run, confirmed by
  deliberately editing a slide's source without re-rendering and watching
  `parity` name every word the stale PNG was missing.

## Model-assisted review (`metrics`, `review`)

A second, independent review lane over the same rendered deck — advisory,
and never a gate, unlike `check`.

- **`metrics -dir dist [-out metrics.json]`** extracts per-slide layout
  metrics from a rendered deck: box geometry (each slide's own direct
  children, in slide-relative coordinates — not page-relative, since slides
  are stacked on one scrollable page on screen), text length, bullet count,
  the distinct rendered font sizes, the distinct text/background colours in
  use (`palette`), density (text-leaf area ÷ slide area), and image count.
  This has standalone value independent of `review`: it is the input to any
  layout review, human or model. The probe lives in its own script,
  `internal/metrics/metrics.js`, deliberately not folded into `probe.js` —
  that script is already refactored into one function per check rule and is
  at its complexity ceiling, and metrics is a different job (reporting
  numbers, not pass/fail findings).
- **`make review` runs `metrics`, then hands the JSON to `ffreis-modelctl`**
  for a text critique:
  `uv run --project <ffreis-modelctl> modelctl run review --task "…" --file
  metrics.json --provider deepseek --workspace ws-shared --root <projects
  root> --json`. `review` is the `modelctl run --list-kinds` kind this uses;
  it was confirmed to exist rather than assumed.
- **`ffreis-modelctl` is text-only — no vision support.** That is exactly why
  this lane reviews layout *metrics* rather than the rendered PNG files. A second
  lane over the images themselves, with a vision model, is deliberately out
  of scope here.
- **`make review` always exits 0.** It prints a model's opinion about layout
  metrics, which is advice, not a measurement — nothing here should ever
  block a build or a merge the way `check`'s findings do. When `modelctl` is
  unavailable or errors, that is printed plainly and the target still exits
  0, but it never prints a fake success: the output makes it obvious no
  critique was obtained. `review` is deliberately not wired into
  `quality-gates`, `ci-local`, or any CI workflow.

## Live preview (`serve`)

`serve` renders the deck to a temporary directory, serves it over loopback
HTTP, and re-renders plus reloads the browser when a source file changes.

- **Binds to `127.0.0.1` by default, never `:8080`.** A deck is unpublished
  client material; binding every interface would put it on the LAN by
  default. `-addr 0.0.0.0:8080` opts in explicitly.
- **Change detection polls an mtime/size fingerprint rather than using
  fsnotify.** That adds no dependency, is immune to the editor-writes-via-
  rename pattern that makes inotify miss some saves, and a deck is a few
  dozen small files — a scan this cheap costs nothing at the poll interval.
  A burst of changes from one save is coalesced by a debounce timer into one
  rebuild.
- **Live reload is a Server-Sent Events stream (`/__reload`) plus a bare
  `EventSource`/`location.reload()` script**, needing no client-side
  library. The script is injected into the served HTML at serve time only,
  in `internal/serve` — `internal/render`'s output, and what `build` writes
  to disk, stay byte-for-byte what ships.
- **A failed re-render keeps serving the last good page with the error
  overlaid**, rather than taking the server down — losing the whole preview
  over a YAML typo mid-edit would make the tool useless.
- The watch set is the deck root (`deck.yaml`, `slides/`, `assets/`,
  `charts/`, `data/`) plus the one theme directory the deck currently
  resolves to, so editing the theme reloads too.

## Contract (`validate`)

`validate` checks a **loaded** deck against its theme's field vocabulary
before anything renders — `deck.Load` itself only fails on a missing
`layout:` key, so without this stage a bad field reaches a Go template and
produces either a blank region or a confusing template error three steps
downstream. `build` runs the same check before generators and render, and
aborts on **fatal** findings; there is no flag to skip it. Both report every
finding in one pass, naming the slide's source file and the offending key,
not just the first problem.

A theme's field vocabulary is declarative, the same idiom as `.Fields`
itself: `internal/contract/contract.yaml` lists, per layout, the keys it
`required`s and the keys it additionally `allowed`s; any other front-matter
key is an error. That file is embedded via `//go:embed` as the compiler's
shipped default, describing the sample theme's layouts. A theme overrides it
wholesale with its own `contract.yaml` at its theme root — adding a layout is
then a YAML change, not a Go change. A `chart:`/`diagram:` block is
recognised on every layout unconditionally, the same way chrome keys are, so
it is never part of a layout's own vocabulary.

Fatal rules: `unknown-layout`, `missing-field`, `unknown-field`,
`missing-asset` (a `media`, or a chart/diagram `spec`/`data`, that does not
exist on disk), `unsafe-path`, `invalid-field` (a malformed chart/diagram
block, or an asset field or `alt` that is not a non-empty string),
`invalid-contract` (a theme's own `contract.yaml` fails to parse), `row-arity`
(a `rows` list whose row lengths disagree with `columns`).

**`required` is key presence, which is why `alt` needed its own rule.**
`alt: ""` — and `alt: "   "` — satisfied `required: [alt]` silently, so a
layout whose image *is* the slide's content could ship with no accessible
name, and no `check` rule covers alt quality either (`missing-asset` fires on
a failed load, never on the text). An empty alt is correct markup only for a
decorative image, and a theme writes that into its own template — the sample
theme's badge does — so it is never something a deck author declares. The
emptiness test is one primitive, `textFinding`, shared with the asset fields
rather than copied: `media`, a chart's `spec`/`data` and `alt` all answer the
same question. The check sits **after** the unknown-field case in the field
switch on purpose; checking alt's content first would exempt the key from the
vocabulary check on every layout that never declared it
(`TestValidateStillReportsAnAltTheLayoutDoesNotDeclare`).

**`row-arity` exists because a well-formed-looking table can still be wrong.**
YAML splits a flow sequence on every top-level comma, so a decimal written
with a comma — `9,8 milhões`, the Portuguese convention — inside an unquoted
table cell silently becomes two cells, and every value after it in that row
lands under the wrong header. The rendered table still fits the slide and
reads as plausible numbers, so none of `check`'s measurements (overflow,
contrast, tiny-type…) ever see a problem; only an arity check catches it.
Quote any cell value that might contain a comma. The rule only looks at a
`rows` element when it is itself a list — a layout that shapes rows as maps
(one row per `{name, price, benefits}`, say) has no cell count to compare
against `columns`, so it is skipped rather than flagged.

**`rows_csv: path/to/file.csv`, on any slide, is the structural fix for the
same bug.** It replaces an inline `rows:` sequence; `deck.Load` reads the CSV
(via `encoding/csv`, so RFC 4180 quoting — not YAML's — governs a comma
inside a value) and fills in `rows` and, unless the slide already declares
its own, `columns` from the file's header row before the slide ever reaches
the contract or a template, so a CSV- and a YAML-authored table are
indistinguishable to either. A ragged CSV row (wrong field count) is a parse
error naming the line — row-arity becomes structurally impossible rather
than a rule that has to catch it after the fact. When a slide declares both
`columns:` and `rows_csv:`, the CSV header must match the declaration
exactly, cell for cell: a column renamed in the CSV without updating
`columns:` fails the build instead of silently misaligning every value under
it, the same failure mode the whole mechanism exists to prevent. A slide
setting both `rows:` and `rows_csv:` is rejected outright — only one may
author a table's rows.

**`undescribed-layout` is the one advisory rule, and the split matters.**
Whether a layout exists is the *theme's* answer — `theme.LayoutNames()` —
never the contract's. Conflating the two shipped a validator that rejected
every deck whose theme has more layouts than the compiler's own examples,
with a report that read `theme "x" has no layout "table" (available: …
table …)`: the decision came from the contract map while the message came
from the theme. A theme is allowed to ship a layout with nothing but a
template — that is the entire point of `.Fields` — so an undescribed layout
costs field checking, not the build. It is reported once per layout rather
than once per slide, names the `contract.yaml` path that would fix it, and
`contract.Fatal` is what callers gate on. Skipping it silently instead
would make the gate vacuous for exactly the themes that most need
describing.

Deliberately not implemented: a
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
seven rules fire**. Without it a validator that silently stopped catching one
of them would look exactly like a clean deck.

## Commands

```bash
go run ./cmd/ffreis-presentation-compiler build \
  -deck-root examples/sample-deck -theme-root examples/themes \
  -out dist -formats html,pdf
go run ./cmd/ffreis-presentation-compiler serve \
  -deck-root examples/sample-deck -theme-root examples/themes -addr 127.0.0.1:8080
go run ./cmd/ffreis-presentation-compiler validate \
  -deck-root examples/sample-deck -theme-root examples/themes
go run ./cmd/ffreis-presentation-compiler doctor

make build             # render the example deck (override DECK= THEMES= OUT= FORMATS=)
make validate-deck     # check the example deck's contract (override DECK= THEMES=)
make validate-selftest # prove the validator still catches all seven rules
make smoke-check       # end-to-end render + pagination assertion
make check             # render the example deck and measure it
make check-selftest    # prove the checker still catches all eight rules
make export-selftest   # prove the generated pptx actually opens (LibreOffice)
make pptx-acceptance PROFILE=<n>  # ... and that real PowerPoint opens it
make seo-selftest      # prove every required meta tag is emitted
make seo-audit         # Lighthouse SEO score against the rendered deck
make golden            # perceptual diff against the approved baseline (examples/golden/)
make golden-update     # re-render and re-approve the baseline (never run automatically)
make metrics           # extract per-slide layout metrics as JSON
make review            # metrics + a model's advisory critique (always exits 0)
make doctor            # which adapters are available here
```

## Declared quality gates (`flags/flags.json`)

`flags/flags.json` is this repo's flag registry: every deliberate point of
variation declared once, in the fleet's schema
(`ffreis-platform-standards/flags/flag-registry.schema.json`). Four
`category: quality` gates are declared here — `font-integrity`,
`image-alt-meaningful`, `contrast-aa`, `output-gate-exists` — each
`default: "error"` with a `seam` naming the code that actually fails.

**The `exceptions` entries are the load-bearing part, not boilerplate.** A
gate that is enforcing *except* in one reasoned case looks identical to an
oversight from outside the code; an exception with a real `reason` is what
turns a considered trade-off into something a reader can audit. The four
recorded here are the font rule skipping families with no `@font-face` (a
system stack the theme chose), the alt rule being blind to a template that
hard-codes an `<img>` its contract never declares, the contrast rule
computing against the nearest background *colour* when the real backdrop is
an image or gradient, and `output-gate-exists` not covering visual regression
because the golden baselines cannot be committed (see the `golden` section).
No `browser-live` target is declared: HTML, PDF, PNG and PPTX are all batch
outputs, and `serve` is an authoring preview whose injected script is a
nine-line live-reload stub with no interaction handlers.

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
- **A failed browser *launch* is retried once** (`chrome.LaunchAttempts`). A
  cold CI runner regularly fails the first attempt with "websocket url
  timeout reached" — Chrome is still coming up when chromedp stops waiting —
  and the identical command succeeds moments later. It took down two correct
  deck builds before being fixed, which is how a green CI stops being
  believed. Only the launch retries: once the page is up, a failure belongs
  to the page or the caller and repeating it just produces the same error
  more slowly. The predicate matches on chromedp's message because it
  returns a plain `errors.New` with no sentinel; if they reword it this
  degrades to the old fail-once behaviour rather than misfiring.
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
internal/golden/                   perceptual PNG diff for visual regression
internal/parity/                   OCR content-parity: rendered PNG vs. source
internal/metrics/                  per-slide layout metrics for review (metrics.js)
internal/engine/                   Renderer port, Runner, PDF/PNG/PPTX adapters
internal/engine/chrome/            CDP client, shared by check + adapters
internal/engine/pptx/              OOXML writer (assets/ holds the static parts)
internal/gen/                      generator hook (charts, diagrams) + cache
internal/serve/                    live-reload preview: render/watch/HTTP loop
internal/seo/                      head meta, schema.org, sitemap, robots (leaf)
internal/logx/                     slog construction
examples/sample-deck/              brand-free fixture deck
examples/golden/                   approved baseline PNG files for `make golden`
examples/themes/sample/            brand-free fixture theme
examples/check-fixture/            deliberately broken deck, for check-selftest
examples/themes/check-fixture/     deliberately broken theme, ditto
examples/contract-fixture/         deliberately broken deck, for validate-selftest
flags/flags.json                   declared points of variation (the quality gates)
acceptance/powerpoint/             real-PowerPoint acceptance tests (opt-in)
scripts/powerpoint-acceptance.sh   host side: Windows VM lifecycle for the above
```

## Roadmap

Implemented: content model, theme + layouts, HTML, PDF, PNG, PPTX (image mode),
the generator hook (python, cmd, mermaid, d2), CSV-sourced table rows
(`rows_csv`), `check` (all eight rules), `validate` (all seven rules), the SEO
surface, `serve`, `doctor`, five self-tests, golden images
(`golden`/`golden-update`), OCR content parity (`parity`), and model-assisted
review (`metrics`/`review`).
Also implemented, but never yet run for real: the PowerPoint acceptance
harness (`acceptance/powerpoint/`).
Next: provision one Windows+Office VM so that harness stops being theory,
ideally two — the current PowerPoint and the older one that refused a file.

## Keeping this file current

- Add facts you had to discover the hard way; that is what this file is for.
- Correct anything here that turns out to be wrong — stale guidance is worse
  than none.
- Update the structure tree when packages are added or renamed.
