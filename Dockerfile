# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.26 AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

WORKDIR /src

COPY --link go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod <<-EOF
	go mod download
EOF

COPY --link . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,id=go-build-${TARGETARCH},target=/root/.cache/go-build <<-EOF
	CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" -o /out/promshim ./cmd/promshim
EOF

FROM gcr.io/distroless/static-debian12:nonroot AS runtime

LABEL org.opencontainers.image.title="promshim-clickhouse"
LABEL org.opencontainers.image.description="PromQL compatibility layer for ClickHouse TimeSeries"
LABEL org.opencontainers.image.licenses="Apache-2.0"
LABEL org.opencontainers.image.source="https://github.com/BadLiveware/promshim-clickhouse"

COPY --link --from=build /out/promshim /usr/local/bin/promshim

EXPOSE 9090
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/promshim"]
