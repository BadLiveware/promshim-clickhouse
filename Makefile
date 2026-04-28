.PHONY: test vet race fmt fmt-check tidy tidy-check lint pre-commit check hooks-install hooks-uninstall harness compliance bench sweep sweep-smoke sweep-estimate-heavy bench-status release-check release-snapshot

GO ?= go
GOLANGCI_LINT ?= golangci-lint
GOFILES := $(shell find . \( -path './.git' -o -path './harness/compliance/prom-compliance' \) -prune -o -name '*.go' -print)

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

race:
	$(GO) test -race ./...

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

pre-commit: fmt-check tidy-check lint test

check: fmt-check tidy-check lint test vet

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
