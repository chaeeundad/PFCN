# Pumat build tasks. Requires Go (see go.mod) and, for the solver image, Docker or Podman.

GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build parser proto test race lint e2e clean

all: build

build: parser
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/pumat ./cmd/pumat

# The QE parser artifact is a reproducible WASI module embedded in the agent.
# Rebuilding it changes its digest, which must then be updated in solver manifests.
parser:
	GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false -ldflags="-s -w -buildid=" \
		-o internal/parser/artifacts/qe-parser.wasm ./cmd/pumat-qe-parser
	@shasum -a 256 internal/parser/artifacts/qe-parser.wasm 2>/dev/null || sha256sum internal/parser/artifacts/qe-parser.wasm

proto:
	protoc -I proto --go_out=. --go_opt=module=github.com/chaeeundad/PFCN proto/pumat.proto

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

lint:
	$(GO) vet ./...
	@test -z "$$(gofmt -l cmd internal pkg)" || (gofmt -l cmd internal pkg; exit 1)

# Two local nodes, real QE container, local OCI registry. See scripts/dev-e2e.sh.
e2e: build
	./scripts/dev-e2e.sh

clean:
	rm -rf bin dist
