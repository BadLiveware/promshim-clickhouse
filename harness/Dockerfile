FROM golang:1.26 AS build
ENV PATH=/usr/local/go/bin:${PATH}
WORKDIR /src

COPY go.mod go.sum ./
RUN /usr/local/go/bin/go mod download
COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 /usr/local/go/bin/go build -o /out/promshim ./cmd/promshim
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 /usr/local/go/bin/go build -o /out/promharness-seed ./cmd/promharness-seed
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 /usr/local/go/bin/go build -o /out/promharness-compare ./cmd/promharness-compare

FROM debian:bookworm-slim AS runtime-base
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app

FROM runtime-base AS promshim
COPY --from=build /out/promshim /usr/local/bin/promshim
ENTRYPOINT ["/usr/local/bin/promshim"]

FROM runtime-base AS promharness-seed
COPY --from=build /out/promharness-seed /usr/local/bin/promharness-seed
ENTRYPOINT ["/usr/local/bin/promharness-seed"]

FROM runtime-base AS promharness-compare
COPY --from=build /out/promharness-compare /usr/local/bin/promharness-compare
COPY harness/corpus /app/harness/corpus
ENTRYPOINT ["/usr/local/bin/promharness-compare"]
