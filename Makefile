.DEFAULT_GOAL := help

GO ?= go
GOLANGCI_LINT ?= golangci-lint
COMMANDS := create-job get-job starter validate recognize extract finish
BINARIES := $(addprefix bin/,$(addsuffix /bootstrap,$(COMMANDS)))

INFRA := infra

.PHONY: help build image-context lint lint-go lint-infra fmt test test-int test-infra synth up down

help:
	@echo make build      - build Lambdas for linux/arm64 into bin/
	@echo make lint       - check Go code and formatting, both modules
	@echo make fmt        - format Go code and imports, both modules
	@echo make test       - run unit tests
	@echo make test-int   - run integration tests against the local services
	@echo make test-infra - run CDK assertion tests
	@echo make up         - start DynamoDB Local and S3Mock
	@echo make down       - stop them and discard their data
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
# Against the containers `make up` starts. The endpoints are passed in
# rather than defaulted in code, so a test run can never reach a real
# account by forgetting to set something.
test-int: up
	DYNAMODB_ENDPOINT=http://127.0.0.1:8000 	S3_ENDPOINT=http://127.0.0.1:9090 	AWS_REGION=ap-southeast-1 	$(GO) test -count=1 -tags=integration ./internal/...

up:
	docker compose up -d --wait

down:
	docker compose down -v

test-infra:
	cd $(INFRA) && $(GO) test ./...

# The recognize function ships as a container image. Its build context is
# build/ocr, which holds the Dockerfile and needs the compiled binary beside
# it; copying rather than pointing the context at bin/ keeps what Docker
# receives down to two files.
image-context: build
	cp bin/recognize/bootstrap $(INFRA)/../build/ocr/bootstrap

# The stack mounts bin/<command> as each Lambda's asset and build/ocr as the
# container image's context, so both have to exist before the app can
# synthesize. Synthesizing a Docker asset also builds the image, which needs
# Docker running.
synth: build image-context
	cd $(INFRA) && npx cdk synth -q
