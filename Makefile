SHELL := /bin/bash

GOFMT ?= gofmt
GOLANGCI_LINT ?= golangci-lint
GITLEAKS ?= gitleaks
GOVULNCHECK ?= govulncheck
# 85, not the fleet-default 90: the uncovered remainder is cli.Run (an os.Exit
# wrapper) and the adapter success paths, which only execute with a browser
# installed. Those are covered by `make smoke-check`, which actually renders a
# PDF, rather than by a unit test that would silently pass on a machine with no
# Chrome. Same reasoning ffreis-resume documents for its own lowered floor.
COVERAGE_MIN ?= 85

LEFTHOOK_VERSION ?= 1.7.10
LEFTHOOK_DIR ?= $(CURDIR)/.bin
LEFTHOOK_BIN ?= $(LEFTHOOK_DIR)/lefthook

MUTATION_PACKAGES ?= ./internal/...
MUTATION_THRESHOLD ?= 60


.PHONY: mutation help \
	fmt fmt-check lint validate test test-race coverage-gate integration-coverage-gate quality-gates \
	hook-generated-drift secrets-scan-staged \
	lefthook-bootstrap lefthook-install lefthook-run lefthook setup \

	ci-list install-act ci-local \
	init-github \
	install build doctor smoke-check

## mutation: run mutation testing with gremlins (slow — CI only)
mutation:
	@which gremlins >/dev/null 2>&1 || go install github.com/go-gremlins/gremlins/cmd/gremlins@latest
	gremlins unleash --threshold-efficacy $(MUTATION_THRESHOLD) $(MUTATION_PACKAGES)

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*## "; printf "Targets:\n"} /^[a-zA-Z0-9_.-]+:.*## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

fmt: ## Format all Go files in place
	$(GOFMT) -w .

fmt-check: ## Fail if Go files are not gofmt-formatted
	@./scripts/hooks/check_required_tools.sh $(GOFMT)
	@out="$$(find . -type f -name '*.go' -not -path './vendor/*' -not -path './.git/*' -print0 | xargs -0 -r $(GOFMT) -l)"; \
	if [ -n "$$out" ]; then \
		echo "Unformatted Go files:"; \
		echo "$$out"; \
		echo "Run: $(GOFMT) -w <files>"; \
		exit 1; \
	fi

lint: ## Run golangci-lint
	@./scripts/hooks/check_required_tools.sh $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run

validate: ## Static analysis and compilation check (go vet + build)
	go vet ./...
	go build -o /dev/null ./...

test: ## Run unit tests
	go test ./...

test-race: ## Run tests with race detector
	go test -race ./...

coverage-gate: ## Run tests with coverage and fail if below COVERAGE_MIN
	@COVERAGE_MIN="$(COVERAGE_MIN)" ./scripts/hooks/check_coverage_gate.sh

integration-coverage-gate: ## Run //go:build integration tests with coverage and fail if below COVERAGE_MIN (no-op if no integration-tagged files exist)
	@COVERAGE_MIN="$(COVERAGE_MIN)" ./scripts/hooks/check_integration_coverage_gate.sh

quality-gates: ## Run strict pre-push quality gates (test + race + coverage + govulncheck)
	@./scripts/hooks/check_required_tools.sh $(GOVULNCHECK)
	$(MAKE) test
	$(MAKE) test-race
	$(MAKE) coverage-gate
	$(GOVULNCHECK) ./...

hook-generated-drift: ## Run generate target if present and fail on drift
	@set -euo pipefail; \
	if $(MAKE) -n generate >/dev/null 2>&1; then \
		$(MAKE) generate; \
		if ! git diff --quiet -- .; then \
			echo "Generated files are out of date. Run 'make generate' and commit updates."; \
			git status --short; \
			exit 1; \
		fi; \
	else \
		echo "No 'generate' target found; skipping generated drift check."; \
	fi

secrets-scan-staged: ## Scan staged diff for secrets
	@./scripts/hooks/check_required_tools.sh $(GITLEAKS)
	$(GITLEAKS) protect --staged --redact


lefthook-bootstrap: ## Download lefthook binary into ./.bin
	LEFTHOOK_VERSION="$(LEFTHOOK_VERSION)" BIN_DIR="$(LEFTHOOK_DIR)" bash ./scripts/bootstrap_lefthook.sh

lefthook-install: lefthook-bootstrap ## Install git hooks if missing
	@if [ -x "$(LEFTHOOK_BIN)" ] && [ -x ".git/hooks/pre-commit" ] && [ -x ".git/hooks/pre-push" ] && [ -x ".git/hooks/commit-msg" ]; then \
		echo "lefthook hooks already installed"; \
		exit 0; \
	fi
	LEFTHOOK="$(LEFTHOOK_BIN)" "$(LEFTHOOK_BIN)" install

lefthook-run: lefthook-bootstrap ## Run hooks (pre-commit + commit-msg + pre-push)
	LEFTHOOK="$(LEFTHOOK_BIN)" "$(LEFTHOOK_BIN)" run pre-commit
	@tmp_msg="$$(mktemp)"; \
	echo "chore(hooks): validate commit-msg hook" > "$$tmp_msg"; \
	LEFTHOOK="$(LEFTHOOK_BIN)" "$(LEFTHOOK_BIN)" run commit-msg -- "$$tmp_msg"; \
	rm -f "$$tmp_msg"
	LEFTHOOK="$(LEFTHOOK_BIN)" "$(LEFTHOOK_BIN)" run pre-push

lefthook: lefthook-bootstrap lefthook-install lefthook-run ## Install hooks and run them

setup: lefthook ## Install hooks and verify dev tools
	@./scripts/hooks/check_required_tools.sh $(GOLANGCI_LINT) $(GITLEAKS) $(GOVULNCHECK) || true

ci-list: ## List local CI workflows
	@ls -1 .github/workflows | sort

# ── Local CI (act-based fallback when GH Actions quota is hit) ───────────────
PLATFORM_STANDARDS_SHA ?= 3c787edb4e96ddea2e86b2add2c32139685e8db7  # v1.2.1
PLATFORM_STANDARDS_RAW ?= https://raw.githubusercontent.com/FelipeFuhr/ffreis-platform-standards

install-act: ## Download pinned act binary into .bin/
	@mkdir -p scripts
	@curl -fsSL "$(PLATFORM_STANDARDS_RAW)/$(PLATFORM_STANDARDS_SHA)/scripts/install_act.sh" \
		-o scripts/install_act.sh && chmod +x scripts/install_act.sh
	@bash ./scripts/install_act.sh

ci-local: ## Run workflows locally via act (GH Actions quota fallback). Args via ARGS=...
	@mkdir -p scripts
	@curl -fsSL "https://raw.githubusercontent.com/FelipeFuhr/ffreis-platform-ci-local/v1.0.0/scripts/run-ci-local.sh" \
		-o scripts/run-ci-local.sh && chmod +x scripts/run-ci-local.sh
	@CI_LOCAL_FINDINGS_REF=v1.0.0 PATH="$(CURDIR)/.bin:$(PATH)" bash ./scripts/run-ci-local.sh $(ARGS)

# ── GitHub repo setup (run once after gh repo create) ────────────────────────
QUALITY_KIT_SCRIPTS ?= /media/ffreis/second/projects/quality-kit/scripts

init-github: ## Apply standard fleet settings to the GitHub repo (SHA-pinning, squash-only, etc.)
	@repo=$$(git remote get-url origin 2>/dev/null | sed -E 's|.*github\.com[:/]||; s|\.git$$||'); \
	[ -n "$$repo" ] || { echo "No GitHub remote found" >&2; exit 1; }; \
	bash "$(QUALITY_KIT_SCRIPTS)/configure-repo-settings.sh" "$$repo"

# ── Domain targets ───────────────────────────────────────────────────────────
BINARY      ?= ffreis-presentation-compiler
EXAMPLE_DECK   ?= examples/sample-deck
EXAMPLE_THEMES ?= examples/themes
SMOKE_OUT   ?= .bin/smoke

install: ## Install the CLI into GOBIN
	@go install ./cmd/$(BINARY)

build: ## Render the example deck (override DECK=/path THEMES=/path OUT=/path)
	@go run ./cmd/$(BINARY) build \
		-deck-root "$(or $(DECK),$(EXAMPLE_DECK))" \
		-theme-root "$(or $(THEMES),$(EXAMPLE_THEMES))" \
		-out "$(or $(OUT),dist)" \
		-formats "$(or $(FORMATS),html,pdf)"

doctor: ## Report which render adapters are available on this machine
	@go run ./cmd/$(BINARY) doctor

smoke-check: ## End-to-end: render the example deck and assert the PDF paginates
	@rm -rf "$(SMOKE_OUT)" && mkdir -p "$(SMOKE_OUT)"
	@go run ./cmd/$(BINARY) build -deck-root "$(EXAMPLE_DECK)" \
		-theme-root "$(EXAMPLE_THEMES)" -out "$(SMOKE_OUT)" -formats html,pdf >/dev/null
	@test -s "$(SMOKE_OUT)/index.html" || { echo "smoke: no index.html" >&2; exit 1; }
	@slides=$$(grep -c '<section' "$(SMOKE_OUT)/index.html"); \
	pdf="$(SMOKE_OUT)/$$(basename $(EXAMPLE_DECK)).pdf"; \
	test -s "$$pdf" || { echo "smoke: no pdf at $$pdf" >&2; exit 1; }; \
	if command -v pdfinfo >/dev/null 2>&1; then \
		pages=$$(pdfinfo "$$pdf" | awk '/^Pages:/ {print $$2}'); \
		size=$$(pdfinfo "$$pdf" | sed -n 's/^Page size: *//p'); \
		[ "$$pages" = "$$slides" ] || { echo "smoke: $$slides slides but $$pages pdf pages" >&2; exit 1; }; \
		case "$$size" in 960*x*540*) ;; *) echo "smoke: unexpected page size: $$size" >&2; exit 1 ;; esac; \
		echo "smoke: ok — $$pages pages at $$size"; \
	else \
		echo "smoke: ok — $$slides slides rendered (install poppler-utils to assert pagination)"; \
	fi
