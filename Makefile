SHELL := /bin/bash

GOFMT ?= gofmt
GOLANGCI_LINT ?= golangci-lint
GITLEAKS ?= gitleaks
GOVULNCHECK ?= govulncheck
# 80, not the fleet-default 90. The uncovered remainder is concentrated in code
# that cannot be unit tested without launching a browser:
#
#   internal/engine/chrome  ~33%  CDP navigation, screenshot capture
#   internal/cli            ~81%  cli.Run is an os.Exit wrapper
#
# Everything decidable has been pulled out of those paths and tested directly —
# chrome.clips, the adapters behind the Capturer and Runner ports, the whole
# pptx writer. What is left is the browser boundary itself, and a unit test that
# launched Chrome would pass or fail by machine (engine_test.go made exactly
# that mistake once).
#
# That boundary is covered for real by three targets that do run a browser:
#   make smoke-check     renders a deck, asserts PDF pages == slides
#   make check           measures the rendered deck
#   make check-selftest  asserts all six check rules fire on a broken fixture
#
# Raise this back toward 90 once the browser surface stops growing; do not lower
# it further to make a change fit.
COVERAGE_MIN ?= 80

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
	install build doctor smoke-check check check-selftest export-selftest \
	validate-deck validate-selftest \
	seo-selftest seo-audit \
	golden golden-update review metrics \
	pptx-acceptance acceptance-test acceptance-selftest

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
CONTRACT_FIXTURE ?= examples/contract-fixture
SMOKE_OUT   ?= .bin/smoke

install: ## Install the CLI into GOBIN
	@go install ./cmd/$(BINARY)

# A raw "html,pdf" cannot sit directly inside $(or ...) below: GNU Make's
# function-argument splitter treats a top-level comma in the call's own text
# as a separator, so $(or $(FORMATS),html,pdf) silently resolved to just
# "html" whenever FORMATS was unset — the comma inside an already-expanded
# variable's value is not re-split, so routing it through DEFAULT_FORMATS
# fixes it.
DEFAULT_FORMATS := html,pdf

build: ## Render the example deck (override DECK=/path THEMES=/path OUT=/path)
	@go run ./cmd/$(BINARY) build \
		-deck-root "$(or $(DECK),$(EXAMPLE_DECK))" \
		-theme-root "$(or $(THEMES),$(EXAMPLE_THEMES))" \
		-out "$(or $(OUT),dist)" \
		-formats "$(or $(FORMATS),$(DEFAULT_FORMATS))"

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

CHECK_OUT ?= .bin/check

check: ## Measure a rendered deck (default: the example deck, rendered first)
	@rm -rf "$(CHECK_OUT)" && mkdir -p "$(CHECK_OUT)"
	@go run ./cmd/$(BINARY) build -deck-root "$(EXAMPLE_DECK)" \
		-theme-root "$(EXAMPLE_THEMES)" -out "$(CHECK_OUT)" -formats html >/dev/null
	@go run ./cmd/$(BINARY) check -dir "$(CHECK_OUT)" -theme-root "$(EXAMPLE_THEMES)" -theme sample

check-selftest: ## Prove the checker is not vacuous: the broken fixture must fail every rule
	@rm -rf "$(CHECK_OUT)-fixture" && mkdir -p "$(CHECK_OUT)-fixture"
	@go run ./cmd/$(BINARY) build -deck-root examples/check-fixture \
		-theme-root "$(EXAMPLE_THEMES)" -out "$(CHECK_OUT)-fixture" -formats html >/dev/null
	@fakepdf="$(CHECK_OUT)-fixture/mismatch.pdf"; \
	printf '%%PDF-1.4\n1 0 obj\n<< /Type /Page >>\nendobj\n' > "$$fakepdf"; \
	out=$$(go run ./cmd/$(BINARY) check -dir "$(CHECK_OUT)-fixture" \
		-theme-root "$(EXAMPLE_THEMES)" -theme check-fixture -pdf "$$fakepdf" 2>&1 || true); \
	missing=""; \
	for rule in overflow out-of-bounds tiny-type contrast missing-asset font-fallback overlap pdf-pages; do \
		echo "$$out" | grep -q "$$rule" || missing="$$missing $$rule"; \
	done; \
	if [ -n "$$missing" ]; then \
		echo "check-selftest: the fixture did not trigger:$$missing" >&2; \
		echo "$$out" >&2; exit 1; \
	fi; \
	echo "check-selftest: ok — all 8 rules fired on the broken fixture"

validate-deck: ## Validate a deck's contract before build (override DECK= THEMES=)
	@go run ./cmd/$(BINARY) validate \
		-deck-root "$(or $(DECK),$(EXAMPLE_DECK))" \
		-theme-root "$(or $(THEMES),$(EXAMPLE_THEMES))"

validate-selftest: ## Prove the validator is not vacuous: the broken fixture must fail every rule
	@out=$$(go run ./cmd/$(BINARY) validate -deck-root "$(CONTRACT_FIXTURE)" \
		-theme-root "$(EXAMPLE_THEMES)" 2>&1 || true); \
	missing=""; \
	for rule in unknown-layout missing-field unknown-field invalid-field missing-asset unsafe-path row-arity; do \
		echo "$$out" | grep -q "$$rule" || missing="$$missing $$rule"; \
	done; \
	if [ -n "$$missing" ]; then \
		echo "validate-selftest: the fixture did not trigger:$$missing" >&2; \
		echo "$$out" >&2; exit 1; \
	fi; \
	echo "validate-selftest: ok — all 7 rules fired on the broken fixture"

export-selftest: ## Prove the PPTX actually opens: round-trip it through LibreOffice
	@command -v soffice >/dev/null 2>&1 || { echo "export-selftest: soffice not installed — skipping" >&2; exit 0; }
	@rm -rf "$(CHECK_OUT)-export" && mkdir -p "$(CHECK_OUT)-export"
	@go run ./cmd/$(BINARY) build -deck-root "$(EXAMPLE_DECK)" \
		-theme-root "$(EXAMPLE_THEMES)" -out "$(CHECK_OUT)-export" -formats html,pptx >/dev/null
	@pptx="$(CHECK_OUT)-export/$$(basename $(EXAMPLE_DECK)).pptx"; \
	test -s "$$pptx" || { echo "export-selftest: no pptx produced" >&2; exit 1; }; \
	soffice --headless --norestore -env:UserInstallation=file://$(CURDIR)/$(CHECK_OUT)-export/lo \
		--convert-to pdf --outdir "$(CHECK_OUT)-export/rt" "$$pptx" >/dev/null 2>&1 || true; \
	pdf="$(CHECK_OUT)-export/rt/$$(basename $(EXAMPLE_DECK)).pdf"; \
	test -s "$$pdf" || { echo "export-selftest: LibreOffice could not open the generated pptx" >&2; exit 1; }; \
	if command -v pdfinfo >/dev/null 2>&1; then \
		pages=$$(pdfinfo "$$pdf" | awk '/^Pages:/ {print $$2}'); \
		slides=$$(grep -c '<section' "$(CHECK_OUT)-export/index.html"); \
		[ "$$pages" = "$$slides" ] || { echo "export-selftest: $$slides slides but $$pages pptx pages" >&2; exit 1; }; \
		echo "export-selftest: ok — pptx opens, $$pages slides"; \
	else \
		echo "export-selftest: ok — pptx opens"; \
	fi

# ── SEO ──────────────────────────────────────────────────────────────────────
SEO_OUT ?= .bin/seo
SEO_PORT ?= 8731
# Pinned, not "latest": a Lighthouse minor can move an audit between categories
# and turn a green gate red with no change to the deck. The fleet's websites
# gate on categories:seo at 0.9, so a deck held to the same floor is comparable.
LIGHTHOUSE_VERSION ?= 12.2.1
LIGHTHOUSE_SEO_MIN ?= 0.9

seo-selftest: ## Prove the SEO emitter is not vacuous: every required tag must be present
	@set -euo pipefail; \
	rm -rf "$(SEO_OUT)" && mkdir -p "$(SEO_OUT)"; \
	go run ./cmd/$(BINARY) build -deck-root "$(EXAMPLE_DECK)" \
		-theme-root "$(EXAMPLE_THEMES)" -out "$(SEO_OUT)" -formats html >/dev/null; \
	index="$(SEO_OUT)/index.html"; \
	missing=""; \
	for tag in 'name="robots"' 'name="description"' 'name="author"' \
	           'rel="canonical"' 'property="og:locale"' 'property="og:type"' \
	           'property="og:title"' 'property="og:description"' \
	           'property="og:url"' 'property="og:image"' \
	           'property="og:image:alt"' 'property="og:site_name"' \
	           'name="twitter:card"' 'name="twitter:title"' \
	           'name="twitter:description"' 'name="twitter:image"' \
	           'application/ld+json'; do \
		grep -qF -- "$$tag" "$$index" || missing="$$missing $$tag"; \
	done; \
	if [ -n "$$missing" ]; then \
		echo "seo-selftest: the rendered deck is missing:$$missing" >&2; exit 1; \
	fi; \
	for f in sitemap.xml robots.txt; do \
		test -s "$(SEO_OUT)/$$f" || { echo "seo-selftest: no $$f" >&2; exit 1; }; \
	done; \
	grep -qF 'Sitemap: ' "$(SEO_OUT)/robots.txt" || \
		{ echo "seo-selftest: robots.txt omits the Sitemap directive" >&2; exit 1; }; \
	python3 -c 'import json,re,sys; \
h=open("$(SEO_OUT)/index.html",encoding="utf-8").read(); \
b=re.search(r"<script type=\"application/ld\+json\">(.*?)</script>",h,re.S); \
sys.exit("seo-selftest: no ld+json block") if not b else None; \
d=json.loads(b.group(1)); \
sys.exit("seo-selftest: ld+json @type is %r" % d.get("@type")) \
  if d.get("@type")!="PresentationDigitalDocument" else None; \
print("seo-selftest: ok — 17 tags, sitemap, robots, and ld+json parses as %s" % d["@type"])'

seo-audit: ## Audit the rendered deck with Lighthouse (SEO category, floor $(LIGHTHOUSE_SEO_MIN))
	@command -v npx >/dev/null 2>&1 || { \
		echo "seo-audit: npx not found — install Node to run the Lighthouse audit." >&2; \
		echo "seo-audit: 'make seo-selftest' covers the emitted vocabulary without Node." >&2; \
		exit 1; }
	@set -euo pipefail; \
	$(MAKE) --no-print-directory seo-selftest >/dev/null; \
	python3 -m http.server "$(SEO_PORT)" --bind 127.0.0.1 --directory "$(SEO_OUT)" >/dev/null 2>&1 & \
	server=$$!; \
	trap 'kill "$$server" 2>/dev/null || true' EXIT; \
	for _ in $$(seq 1 40); do \
		curl -fsS "http://127.0.0.1:$(SEO_PORT)/" >/dev/null 2>&1 && break; \
		sleep 0.25; \
	done; \
	curl -fsS "http://127.0.0.1:$(SEO_PORT)/" >/dev/null || \
		{ echo "seo-audit: the local server never came up on $(SEO_PORT)" >&2; exit 1; }; \
	npx --yes lighthouse@$(LIGHTHOUSE_VERSION) "http://127.0.0.1:$(SEO_PORT)/" \
		--only-categories=seo --output=json --output-path="$(SEO_OUT)/lighthouse.json" \
		--chrome-flags="--headless=new --no-sandbox --disable-dev-shm-usage" \
		--quiet >/dev/null; \
	python3 -c 'import json,sys; \
r=json.load(open("$(SEO_OUT)/lighthouse.json",encoding="utf-8")); \
score=r["categories"]["seo"]["score"]; \
failed=[a["id"] for a in r["audits"].values() if a.get("score") == 0]; \
print("seo-audit: score %.2f (floor $(LIGHTHOUSE_SEO_MIN))" % score); \
[print("  failed: %s" % f) for f in failed]; \
sys.exit(0 if score >= $(LIGHTHOUSE_SEO_MIN) else "seo-audit: below the floor")'

# ── Golden images (visual regression) ────────────────────────────────────────
# Nothing else in this fleet owns visual regression; this is new, minimal
# ground. Comparison is perceptual (internal/golden), not byte-exact: Chrome's
# PNG encoder is not reproducible across versions, and a byte compare would
# fail on every Chrome update and teach everyone to re-approve blindly.
GOLDEN_BASELINE ?= examples/golden
GOLDEN_RENDER   ?= .bin/golden-render
GOLDEN_DIFF     ?= .bin/golden

golden: ## Compare the rendered example deck's PNG files against the approved baseline (perceptual diff)
	@rm -rf "$(GOLDEN_RENDER)" && mkdir -p "$(GOLDEN_RENDER)"
	@go run ./cmd/$(BINARY) build -deck-root "$(EXAMPLE_DECK)" \
		-theme-root "$(EXAMPLE_THEMES)" -out "$(GOLDEN_RENDER)" -formats png >/dev/null
	@go run ./cmd/$(BINARY) golden -dir "$(GOLDEN_RENDER)/slides" \
		-baseline "$(GOLDEN_BASELINE)" -diff-dir "$(GOLDEN_DIFF)"

# Deliberately plain `cp`, not a Go code path: the compare target above (make
# golden) has no flag that writes to GOLDEN_BASELINE at all, so a typo in a
# compare invocation can never silently re-approve the baseline — only this
# target, run by name, ever touches it.
golden-update: ## Re-render and re-approve the golden baseline (never run automatically)
	@rm -rf "$(GOLDEN_RENDER)" && mkdir -p "$(GOLDEN_RENDER)"
	@go run ./cmd/$(BINARY) build -deck-root "$(EXAMPLE_DECK)" \
		-theme-root "$(EXAMPLE_THEMES)" -out "$(GOLDEN_RENDER)" -formats png >/dev/null
	@mkdir -p "$(GOLDEN_BASELINE)"
	@rm -f "$(GOLDEN_BASELINE)"/*.png
	@for f in "$(GOLDEN_RENDER)"/slides/*.png; do \
		name=$$(basename "$$f"); \
		cp "$$f" "$(GOLDEN_BASELINE)/$$name"; \
		echo "golden-update: wrote $(GOLDEN_BASELINE)/$$name"; \
	done

# ── Model-assisted review (advisory, never a gate) ───────────────────────────
METRICS_OUT ?= .bin/metrics.json
MODELCTL_PROJECT ?= /media/ffreis/second/projects/ffreis-modelctl
MODELCTL_ROOT    ?= /media/ffreis/second/projects
MODELCTL_WORKSPACE ?= ws-shared

metrics: ## Render the example deck and extract per-slide layout metrics as JSON
	@rm -rf "$(CHECK_OUT)-metrics" && mkdir -p "$(CHECK_OUT)-metrics"
	@go run ./cmd/$(BINARY) build -deck-root "$(EXAMPLE_DECK)" \
		-theme-root "$(EXAMPLE_THEMES)" -out "$(CHECK_OUT)-metrics" -formats html >/dev/null
	@go run ./cmd/$(BINARY) metrics -dir "$(CHECK_OUT)-metrics" -out "$(METRICS_OUT)"
	@echo "metrics: wrote $(METRICS_OUT)"

# review is advisory, never a gate: it is a model's opinion on layout metrics,
# not a measurement, so it must never block a build or a merge the way `check`
# does. It always exits 0 — a critique that failed to arrive is reported
# plainly on stdout/stderr, never faked as a pass, but it is still not a
# reason to fail `make review` itself. Not wired into quality-gates, ci-local,
# or any CI workflow for the same reason.
review: ## Extract layout metrics and get a model's advisory critique (never blocking)
	@$(MAKE) --no-print-directory metrics
	@if ! command -v uv >/dev/null 2>&1; then \
		echo "review: uv not found — cannot run ffreis-modelctl; no critique obtained"; \
		exit 0; \
	fi; \
	if [ ! -d "$(MODELCTL_PROJECT)" ]; then \
		echo "review: $(MODELCTL_PROJECT) not found — no critique obtained"; \
		exit 0; \
	fi; \
	uv run --project "$(MODELCTL_PROJECT)" modelctl run review \
		--task "Review these rendered-slide layout metrics for a presentation deck. Flag slides that look crowded (high density), inconsistent (font sizes or palette jumping between slides), or sparse, and suggest what to fix." \
		--file "$(METRICS_OUT)" \
		--provider deepseek --workspace "$(MODELCTL_WORKSPACE)" \
		--root "$(MODELCTL_ROOT)" --json \
	&& echo "review: critique above is advisory only — it never fails the build" \
	|| echo "review: modelctl did not return a critique (see output above, if any) — no critique obtained, this is not a pass"; \
	exit 0

# ── PowerPoint acceptance (optional; needs a provisioned Windows VM) ─────────
#
# The only check in this repo that asks desktop Microsoft PowerPoint whether it
# will open what we wrote. Everything else -- pptx_test.go's byte assertions,
# export-selftest's LibreOffice round-trip, python-pptx -- is a lenient reader,
# and a file all of them accepted was still refused by a real PowerPoint.
#
# Needs a licensed Windows + Office VM that cannot live in this repo, so this is
# never part of quality-gates or CI. See acceptance/powerpoint/README.md.
PPT_ACCEPT_OUT ?= .bin/powerpoint
ACCEPTANCE_DIR ?= acceptance/powerpoint
# Kept in step with acceptance/powerpoint/requirements.txt by hand: there is
# no Python dependency resolver in this repo to derive it from.
ACCEPTANCE_PYTEST ?= 9.0.3
PROFILE ?=
PPTX    ?=
SLIDES  ?=

acceptance-test: ## Run the PowerPoint harness's own portable tests (no Windows needed)
	@command -v uv >/dev/null 2>&1 || { \
		echo "acceptance-test: uv is not installed -- this target FAILS rather than" >&2; \
		echo "  skipping, because a skip that reads as a pass is how a gate stops gating." >&2; \
		exit 1; \
	}
	@cd $(ACCEPTANCE_DIR) && uv run --no-project --with "pytest==$(ACCEPTANCE_PYTEST)" -- \
		python -m pytest -q -p no:cacheprovider
	@echo "acceptance-test: ok -- the Windows-only tests skipped, as they must off-Windows"

acceptance-selftest: ## Prove the acceptance orchestrator works, without Windows
	@./$(ACCEPTANCE_DIR)/selftest.sh

pptx-acceptance: ## Open the built pptx in real PowerPoint in a VM: make pptx-acceptance PROFILE=<name>
	@[ -n "$(PROFILE)" ] || { \
		echo "usage: make pptx-acceptance PROFILE=<name> [PPTX=<file>] [SLIDES=<n>]" >&2; \
		echo "       profiles live in acceptance/powerpoint/profiles/<name>.env" >&2; \
		ls -1 acceptance/powerpoint/profiles/*.env 2>/dev/null | sed 's/^/       have: /' >&2; \
		exit 2; \
	}
	@set -euo pipefail; \
	pptx="$(PPTX)"; slides="$(SLIDES)"; \
	if [ -z "$$pptx" ]; then \
		rm -rf "$(PPT_ACCEPT_OUT)/build" && mkdir -p "$(PPT_ACCEPT_OUT)/build"; \
		go run ./cmd/$(BINARY) build -deck-root "$(EXAMPLE_DECK)" \
			-theme-root "$(EXAMPLE_THEMES)" -out "$(PPT_ACCEPT_OUT)/build" \
			-formats html,pptx >/dev/null; \
		pptx="$(PPT_ACCEPT_OUT)/build/$$(basename $(EXAMPLE_DECK)).pptx"; \
		test -s "$$pptx" || { echo "pptx-acceptance: no pptx produced" >&2; exit 3; }; \
		if [ -z "$$slides" ]; then \
			slides="$$(grep -c '<section' "$(PPT_ACCEPT_OUT)/build/index.html")"; \
		fi; \
	fi; \
	args="--profile $(PROFILE) --pptx $$pptx --artifacts $(PPT_ACCEPT_OUT)/$(PROFILE)"; \
	if [ -n "$$slides" ]; then args="$$args --slides $$slides"; fi; \
	echo "pptx-acceptance: $$pptx (slides: $${slides:-not asserted}) -> profile $(PROFILE)"; \
	exec ./scripts/powerpoint-acceptance.sh $$args
