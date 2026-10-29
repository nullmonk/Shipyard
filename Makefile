# Shipyard local build driver.
#
#   make build   - static, stripped binary at bin/shipyard
#   make test    - go test ./...
#
# The root shipfile.py calls these same targets inside the build
# container, so keep them hermetic: `make build` must need nothing but
# the Go toolchain and this repo.

GO ?= go
BIN ?= bin/shipyard

# CGO off => pure static binary (no libc dependency to drag around).
CGO_ENABLED ?= 0

# -trimpath drops local paths from the binary; -s -w strips symbol
# table and debug info. Together with CGO_ENABLED=0 this yields a
# small, fully static, stripped binary.
GOFLAGS := -trimpath
LDFLAGS := -s -w

.PHONY: all build test vet clean size help

all: vet test build

build:
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(BIN) ./cmd/shipyard

test:
	CGO_ENABLED=$(CGO_ENABLED) $(GO) test ./...

vet:
	$(GO) vet ./...

clean:
	rm -rf bin dist

# Report what build produced: size plus static/stripped confirmation.
size: build
	ls -lh $(BIN)
	file $(BIN)

help:
	@echo "Targets:"
	@echo "  build   build static stripped binary at $(BIN)"
	@echo "  test    run go test ./..."
	@echo "  vet     run go vet ./..."
	@echo "  clean   remove bin/ and dist/"
	@echo "  size    build, then show binary size and linkage"
