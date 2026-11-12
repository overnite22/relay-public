VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/overnite22/relay-public/internal/api.Version=$(VERSION)

.PHONY: web build test fmt dev
web:
	cd web && npm ci --no-audit --no-fund && npm run build

build: web
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/relay ./cmd/relay

test:
	test -z "$$(gofmt -l ./cmd ./internal ./web)"
	go vet ./...
	go test ./...

fmt:
	gofmt -w ./cmd ./internal ./web

# Instance locale sans SSO (ne jamais exposer) ; interface en direct : cd web && npm run dev
dev:
	RELAY_AUTH_MODE=dev RELAY_DEV_USER=dev@local RELAY_DATA_DIR=./data RELAY_LISTEN=127.0.0.1:8080 \
	RELAY_GUACD_ADDR=127.0.0.1:4822 go run ./cmd/relay
