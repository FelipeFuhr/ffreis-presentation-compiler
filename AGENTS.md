# Agent Context

**This repo:** `ffreis-presentation-compiler` — Compiles declarative slide decks (YAML + Markdown + a brand theme) to HTML, PDF and PNG.

## Non-obvious facts

- **Test coverage minimum:** `COVERAGE_MIN=90` — enforced locally via
  `make coverage-gate` (pre-push) and in CI via `coverage.yml`.
- **Mutation testing:** Runs monthly against `./internal/...` with a
  `60%` efficacy threshold. Triggered by `mutation.yml`.
- **lefthook hooks** pull from `ffreis-platform-standards` (pinned SHA) and run
  `make quality-gates` on pre-push. Install with `make setup`.
- **Renovate** keeps Go module dependencies and GitHub Actions SHAs updated automatically.
  It extends `ffreis-platform-standards:renovate/go`.

## Structure

```
cmd/ffreis-presentation-compiler/    ← CLI entry point
internal/               ← feature packages (tested, linted, covered)
scripts/hooks/          ← pre-commit and pre-push hook scripts
.github/workflows/      ← CI/CD workflows
```

## Build and run

```bash
make setup              # install lefthook git hooks
make test               # run unit tests
make coverage-gate      # run tests + enforce coverage minimum
make quality-gates      # full pre-push gate: test + race + coverage + govulncheck
make lint               # run golangci-lint
go run ./cmd/ffreis-presentation-compiler --help
```

## Keeping this file current

- **If you discover a fact not reflected here:** add it before finishing your task.
- **If something here is wrong or outdated:** correct it in the same commit as the code change.
- **If you rename a file, command, or concept referenced here:** update the reference.
