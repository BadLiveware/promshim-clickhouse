.PHONY: build test test-report integration-test integration-test-report vet race test-race fmt fmt-check tidy tidy-check lint script-check config-check pre-commit check hooks-install hooks-uninstall harness compliance bench sweep sweep-smoke sweep-estimate-heavy bench-status release-check release-snapshot

GO ?= go
GOLANGCI_LINT ?= golangci-lint
GOTESTSUM ?= gotestsum
PYTHON ?= python3
SHELLCHECK ?= shellcheck
YAMLLINT ?= yamllint
GOFILES := $(shell find . \( -path './.git' -o -path './harness/compliance/prom-compliance' \) -prune -o -name '*.go' -print)
SHELLFILES := $(shell git ls-files '*.sh' ':(exclude)harness/compliance/prom-compliance/**')
PYFILES := $(shell git ls-files '*.py' ':(exclude)harness/compliance/prom-compliance/**')
YAMLFILES := $(shell git ls-files '*.yml' '*.yaml' ':(exclude)harness/compliance/prom-compliance/**')
JSONFILES := $(shell git ls-files '*.json' ':(exclude).agents/**' ':(exclude)harness/compliance/prom-compliance/**')
UNIT_PKGS := $(shell $(GO) list ./... | grep -v '/integration/')
UNIT_TEST_FLAGS ?= -skip Integration
# integration/promshim currently expects a live scrape-style fixture; the
# compliance stack reliably supports the ClickHouse storage integration suite.
INTEGRATION_PKGS := ./internal/promshim/storage
INTEGRATION_ENV := PROM_SHIM_RUN_INTEGRATION_TESTS=1 PROM_SHIM_CLICKHOUSE_ENDPOINT=http://127.0.0.1:28123/ PROM_SHIM_CLICKHOUSE_NATIVE_ADDR=127.0.0.1:29000 PROM_SHIM_CLICKHOUSE_TRANSPORT=native

build:
	$(GO) build ./...

test:
	$(GO) test $(UNIT_TEST_FLAGS) $(UNIT_PKGS)

test-report:
	@command -v $(GOTESTSUM) >/dev/null 2>&1 || { \
		echo "gotestsum is required; install it from https://github.com/gotestyourself/gotestsum" >&2; \
		exit 127; \
	}
	mkdir -p harness/artifacts/unit
	$(GOTESTSUM) --junitfile harness/artifacts/unit/junit-go.xml -- $(UNIT_TEST_FLAGS) $(UNIT_PKGS)

integration-test:
	$(INTEGRATION_ENV) $(GO) test $(INTEGRATION_PKGS)

integration-test-report:
	@command -v $(GOTESTSUM) >/dev/null 2>&1 || { \
		echo "gotestsum is required; install it from https://github.com/gotestyourself/gotestsum" >&2; \
		exit 127; \
	}
	mkdir -p harness/artifacts/integration
	$(INTEGRATION_ENV) $(GOTESTSUM) --junitfile harness/artifacts/integration/junit-integration.xml -- $(INTEGRATION_PKGS)

vet:
	$(GO) vet ./...

race:
	$(GO) test -race ./...

test-race:
	$(GO) test -race $(UNIT_TEST_FLAGS) $(UNIT_PKGS)

fmt:
	gofmt -w $(GOFILES)

fmt-check:
	@test -z "$$(gofmt -l $(GOFILES))" || (gofmt -l $(GOFILES); exit 1)

tidy:
	$(GO) mod tidy

tidy-check:
	$(GO) mod tidy -diff

lint:
	@command -v $(GOLANGCI_LINT) >/dev/null 2>&1 || { \
		echo "golangci-lint is required; install it from https://golangci-lint.run/usage/install/" >&2; \
		exit 127; \
	}
	$(GOLANGCI_LINT) run ./...

script-check:
	@command -v $(SHELLCHECK) >/dev/null 2>&1 || { \
		echo "shellcheck is required; install it from https://www.shellcheck.net/" >&2; \
		exit 127; \
	}
	@command -v $(PYTHON) >/dev/null 2>&1 || { \
		echo "python3 is required for Python syntax checks" >&2; \
		exit 127; \
	}
	$(SHELLCHECK) -x --severity=warning $(SHELLFILES)
	$(PYTHON) -c 'import pathlib, sys; [compile(pathlib.Path(path).read_text(encoding="utf-8"), path, "exec") for path in sys.argv[1:]]' $(PYFILES)

config-check:
	@command -v $(PYTHON) >/dev/null 2>&1 || { \
		echo "python3 is required for JSON validation" >&2; \
		exit 127; \
	}
	@command -v $(YAMLLINT) >/dev/null 2>&1 || { \
		echo "yamllint is required; install it from https://yamllint.readthedocs.io/" >&2; \
		exit 127; \
	}
	@command -v docker >/dev/null 2>&1 || { \
		echo "docker is required for docker compose config validation" >&2; \
		exit 127; \
	}
	$(PYTHON) -c 'import json, sys; [json.load(open(path, encoding="utf-8")) for path in sys.argv[1:]]' $(JSONFILES)
	$(YAMLLINT) -d '{extends: relaxed, rules: {truthy: disable, line-length: disable}}' $(YAMLFILES)
	docker compose -f harness/docker-compose.yml config >/dev/null
	docker compose -f harness/compliance/docker-compose.yml -f harness/compliance/docker-compose.native-only.yml config >/dev/null
	docker compose -f harness/bench/docker-compose.yml config >/dev/null
	docker compose -f harness/bench/docker-compose.yml -f harness/bench/docker-compose.reference.yml config >/dev/null

pre-commit: fmt-check tidy-check lint script-check config-check build test

check: fmt-check tidy-check lint script-check config-check build test vet

hooks-install:
	git config core.hooksPath .githooks
	@echo "Installed repository hooks from .githooks"

hooks-uninstall:
	@if [ "$$(git config --get core.hooksPath)" = ".githooks" ]; then \
		git config --unset core.hooksPath; \
		echo "Removed repository hooksPath"; \
	else \
		echo "core.hooksPath is not .githooks; leaving it unchanged"; \
	fi

harness:
	./scripts/run-harness.sh

compliance:
	./scripts/run-compliance.sh

bench:
	./scripts/run-bench.sh --matrix

sweep:
	./scripts/run-sweep.sh

sweep-smoke:
	./scripts/run-sweep.sh --name smoke --dry-run --estimate --skip-compliance

sweep-estimate-heavy:
	./scripts/run-sweep.sh --profile all --density dense --corpus-set processing --estimate

bench-status:
	./scripts/run-sweep.sh --bench-status

release-check:
	docker run --rm -v "$(CURDIR):/src" -w /src goreleaser/goreleaser:latest check

release-snapshot:
	docker run --rm -v "$(CURDIR):/src" -w /src -v /var/run/docker.sock:/var/run/docker.sock goreleaser/goreleaser:latest release --snapshot --clean --skip=publish
