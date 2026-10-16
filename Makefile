.DEFAULT_GOAL := help

GO ?= go
GOLANGCI_LINT ?= golangci-lint
COMMANDS := create-job get-job starter validate finish
BINARIES := $(addprefix bin/,$(addsuffix /bootstrap,$(COMMANDS)))

INFRA := infra

.PHONY: help build lint lint-go lint-infra fmt test test-infra synth

help:
	@echo make build      - build Lambdas for linux/arm64 into bin/
	@echo make lint       - check Go code and formatting, both modules
	@echo make fmt        - format Go code and imports, both modules
	@echo make test       - run unit tests
	@echo make test-infra - run CDK assertion tests
	@echo make synth      - synthesize the CDK app \(builds the assets first\)

build: export GOOS := linux
build: export GOARCH := arm64
build: export CGO_ENABLED := 0
build: $(BINARIES)

# Always rebuild so changes in shared internal packages are included.
.PHONY: $(BINARIES)
$(BINARIES): bin/%/bootstrap:
	$(GO) build -trimpath -o $@ ./cmd/$*

lint: lint-go lint-infra

lint-go:
	$(GOLANGCI_LINT) run ./...
	$(GOLANGCI_LINT) fmt --diff ./...

# Split out so CI can lint the two modules in the jobs that already have their
# dependencies; infra/ pulls the whole CDK tree.
lint-infra:
	cd $(INFRA) && $(GOLANGCI_LINT) run ./...
	cd $(INFRA) && $(GOLANGCI_LINT) fmt --diff ./...

fmt:
	$(GOLANGCI_LINT) fmt ./...
	cd $(INFRA) && $(GOLANGCI_LINT) fmt ./...

test:
	$(GO) test ./...

# Separated from `test` because synthesizing runs the CDK's JavaScript in a
# Node subprocess through jsii, which takes seconds rather than milliseconds.
test-infra:
	cd $(INFRA) && $(GO) test ./...

# The stack mounts bin/<command> as each Lambda's asset, so the binaries have
# to exist before the app can synthesize.
synth: build
	cd $(INFRA) && npx cdk synth -q
