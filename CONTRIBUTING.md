# Contributing to ffreis-presentation-compiler

Thank you for your interest in contributing! This guide will help you get started.

## Getting Started

### Prerequisites

See [AGENTS.md](AGENTS.md) for:

- Required tools and versions
- Development environment setup
- Build and test commands

### Local Development

```bash
# Clone the repository
git clone https://github.com/FelipeFuhr/ffreis-presentation-compiler.git
cd ffreis-presentation-compiler

# Set up your environment
make setup

# Verify the setup
make ci
```

## Development Workflow

1. **Create a branch** — use a descriptive name with a prefix:
   - `feat/` for new features
   - `fix/` for bug fixes
   - `chore/` for maintenance
   - `docs/` for documentation

2. **Make your changes** — follow the project's code style and conventions
   (enforced by lefthook)

3. **Test locally** — before pushing:

   ```bash
   make lint
   make test
   ```

4. **Submit a draft PR** — new PRs must start as drafts:

   ```bash
   git push -u origin <branch>
   gh pr create --draft
   ```

5. **Convert to ready when tests pass** — only convert when:
   - All CI checks pass
   - Code review approved
   - You're ready to merge

## Pull Request Requirements

Your PR must satisfy:

- ✅ **Conventional Commits** — PR title follows `type(scope): description`
  - Examples: `feat(auth): add session tokens`, `fix(api): handle null responses`
  - Types: `feat`, `fix`, `chore`, `docs`, `test`, `refactor`, `perf`

- ✅ **Code Quality** — passes `make ci`:
  - Formatting (gofmt/rustfmt/ruff)
  - Linting (golangci-lint/clippy/ruff)
  - Tests with coverage thresholds
  - Security checks (govulncheck/cargo-deny/bandit)

- ✅ **Markdown & Spelling** — no typos or prose issues
  - Tested via markdownlint + typos workflow

- ✅ **GitHub Actions compliance** — all workflows:
  - SHA-pinned actions (no floating `main` or `@latest`)
  - Least-privilege permissions
  - Draft-aware gating

## Testing

All changes must be tested:

```bash
make test          # Run all tests
make test-race     # Check for race conditions (Go)
make coverage      # Generate coverage report
```

Prefer:

- Unit tests for individual functions
- Integration tests for workflows
- Clear test names that describe the scenario

## Code Review

- Reviews are collaborative — ask questions if unclear
- Author is responsible for resolving feedback
- One approval required before merge
- CI must pass entirely

## Reporting Issues

Found a bug or have a feature request?

1. Check [existing issues](../../issues) first
2. Create a new issue with:
   - Clear title and description
   - Steps to reproduce (for bugs)
   - Expected vs actual behavior
   - Environment (OS, version, etc.)

## License

By contributing, you agree that your contributions will be licensed under the
same license as the project.

---

Questions? Check [AGENTS.md](AGENTS.md) or open a discussion.
