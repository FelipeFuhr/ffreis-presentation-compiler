# ffreis-presentation-compiler

<!-- ffreis-badges:start -->
[![CI](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/FelipeFuhr/ffreis-badges/main/badges/ffreis-presentation-compiler/ci.json)](https://github.com/FelipeFuhr/ffreis-presentation-compiler/actions) [![License](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/FelipeFuhr/ffreis-badges/main/badges/ffreis-presentation-compiler/license.json)](https://github.com/FelipeFuhr/ffreis-presentation-compiler/blob/main/LICENSE)
<!-- ffreis-badges:end -->

Compiles declarative slide decks (YAML + Markdown + a brand theme) to HTML, PDF and PNG.

A slide is front-matter plus prose, not a pile of coordinates:

```markdown
---
layout: table
kicker: Benchmark
title: Unit economics against comparable sites
columns: [Site, Units / week, Unit cost, Lead time]
rows:
  - [Site A (ours), "12,400", "R$ 8.10", "6.2 days"]
  - [Site B, "11,900", "R$ 8.90", "4.1 days"]
highlight_row: 1
source: Internal operations reporting, Q3.
---
```

The browser handles layout, text reflow and pagination; the theme owns the
design; the compiler owns page geometry, so one slide is always exactly one PDF
page.

## Quick start

```bash
make doctor        # check a browser is available for the PDF path
make build         # render the bundled example deck into dist/
make smoke-check   # render + assert PDF page count equals slide count
```

Against your own content:

```bash
go run ./cmd/ffreis-presentation-compiler build \
  -deck-root path/to/deck -theme-root path/to/themes \
  -out dist -formats html,pdf
```

The engine is consumer-agnostic: decks and themes are flags, and no brand is
hardcoded. See [`AGENTS.md`](AGENTS.md) for the deck/theme contract and
`examples/` for a working pair.

## Development

This repo follows the fleet standards — git hooks (lefthook) and CI are consumed
from `ffreis-platform-standards` / `ffreis-workflows-*` by pinned reference. Where
a `Makefile` is present, `make setup` installs the hooks and `make ci` runs the
local gate (format, lint, test). See [`AGENTS.md`](AGENTS.md) for this repo's
conventions and the full set of targets.

## Badges

CI / version / license badges above are served from the public
[`ffreis-badges`](https://github.com/FelipeFuhr/ffreis-badges) mirror, so they
render even while this repo is private. They populate once this repo is in the
mirror's manifest (the poller refreshes on a schedule).

## License

See [`LICENSE`](LICENSE).
