# ffreis-presentation-compiler

<!-- ffreis-badges:start -->
[![CI](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/FelipeFuhr/ffreis-badges/main/badges/ffreis-presentation-compiler/ci.json)](https://github.com/FelipeFuhr/ffreis-presentation-compiler/actions) [![License](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/FelipeFuhr/ffreis-badges/main/badges/ffreis-presentation-compiler/license.json)](https://github.com/FelipeFuhr/ffreis-presentation-compiler/blob/main/LICENSE)
<!-- ffreis-badges:end -->

Compiles declarative slide decks (YAML + Markdown + a brand theme) to HTML, PDF and PNG.

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
