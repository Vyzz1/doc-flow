.DEFAULT_GOAL := help

GO ?= go
GOLANGCI_LINT ?= golangci-lint
COMMANDS := create-job get-job starter worker
BINARIES := $(addprefix bin/,$(addsuffix /bootstrap,$(COMMANDS)))

.PHONY: help build lint fmt test

help:
	@echo make build - build Lambda placeholders for linux/arm64 into bin/
	@echo make lint  - check Go code and formatting
	@echo make fmt   - format Go code and imports
	@echo make test  - run Go unit tests

build: export GOOS := linux
build: export GOARCH := arm64
build: export CGO_ENABLED := 0
build: $(BINARIES)

# Always rebuild so changes in shared internal packages are included.
.PHONY: $(BINARIES)
$(BINARIES): bin/%/bootstrap:
	$(GO) build -trimpath -o $@ ./cmd/$*

lint:
	$(GOLANGCI_LINT) run ./...
	$(GOLANGCI_LINT) fmt --diff ./...

fmt:
	$(GOLANGCI_LINT) fmt ./...

test:
	$(GO) test ./...
