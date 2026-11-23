# Build, check and run Stack Overflow. "make help" lists the targets.
#
# "make build" never touches the binary of a running server: it writes
# $(BIN).new. "make install" then renames that file over $(BIN) in one
# step (an atomic rename in the same folder). A server that runs keeps its
# old binary until it restarts.

GO      ?= go
BIN     ?= bin/stack-overflow
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: help build install check serve version

help:
	@echo "make build    build $(BIN).new (the binary in use does not change)"
	@echo "make install  build, then replace $(BIN) in one atomic step"
	@echo "make check    gofmt, go vet and the tests with the race detector"
	@echo "make serve    run scripts/serve.sh (keeps the Mac awake, restarts after a crash, writes a log)"
	@echo "              pass server flags with ARGS, for example: make serve ARGS='-admin pin:alice'"
	@echo "make version  print the version that a build gets"

build:
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN).new ./cmd/stack-overflow

install: build
	mv -f $(BIN).new $(BIN)
	@echo "installed $(BIN) ($(VERSION)); restart the server to use it"

check:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)
	$(GO) vet ./...
	$(GO) test -race -count=1 ./...

serve:
	STACK_OVERFLOW_BIN=$(abspath $(BIN)) scripts/serve.sh $(ARGS)

version:
	@echo $(VERSION)
