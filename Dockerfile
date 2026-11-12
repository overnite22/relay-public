# syntax=docker/dockerfile:1
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/index.html web/tsconfig.json web/vite.config.ts ./
COPY web/src/ ./src/
COPY web/public/ ./public/
COPY scripts/collect-licenses.mjs /collect-licenses.mjs
RUN npm run build && node /collect-licenses.mjs node_modules /npm-licenses.txt

FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY web/assets.go ./web/
COPY --from=web /src/web/dist ./web/dist
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags "-s -w -X github.com/overnite22/relay-public/internal/api.Version=${VERSION}" \
    -o /out/relay ./cmd/relay

RUN find /go/pkg/mod -type f -iname 'license*' -exec sh -c 'for f do printf "\n=== %s ===\n" "$f"; cat "$f"; done' sh {} + > /out/go-licenses.txt

FROM gcr.io/distroless/static-debian12:nonroot
ARG VERSION=dev
LABEL org.opencontainers.image.title="Relay" \
      org.opencontainers.image.source="https://github.com/overnite22/relay-public" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version=$VERSION
COPY --from=build /out/relay /relay
COPY LICENSE NOTICE THIRD_PARTY_NOTICES.md /licenses/
COPY --from=web /npm-licenses.txt /licenses/
COPY --from=build /out/go-licenses.txt /licenses/
ENV RELAY_DATA_DIR=/data RELAY_LISTEN=:8080
EXPOSE 8080
USER nonroot
HEALTHCHECK --interval=30s --timeout=5s CMD ["/relay", "healthcheck"]
ENTRYPOINT ["/relay"]
