.PHONY: test vet race fmt fmt-check tidy tidy-check check harness compliance bench release-check release-snapshot

GO ?= go
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
	@cp go.mod go.mod.check
	@cp go.sum go.sum.check
	@$(GO) mod tidy
	@diff -u go.mod.check go.mod
	@diff -u go.sum.check go.sum
	@rm -f go.mod.check go.sum.check

check: fmt-check tidy-check test vet

harness:
	./scripts/run-harness.sh

compliance:
	./scripts/run-compliance.sh

bench:
	./scripts/run-bench.sh --matrix

release-check:
	docker run --rm -v "$(CURDIR):/src" -w /src goreleaser/goreleaser:latest check

release-snapshot:
	docker run --rm -v "$(CURDIR):/src" -w /src -v /var/run/docker.sock:/var/run/docker.sock goreleaser/goreleaser:latest release --snapshot --clean --skip=publish
