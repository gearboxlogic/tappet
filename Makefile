BUILD_DIR := ./build
BUILD := $(shell git rev-parse --short HEAD)@$(shell date +%s)
LD_FLAGS := -ldflags "-X main.BuildVersion=$(BUILD)"
GO_BUILD := CGO_ENABLED=0 go build $(LD_FLAGS)
CONTAINER_ENGINE ?= docker

.PHONY: build
build:
	mkdir -p $(BUILD_DIR)
	$(GO_BUILD) -o $(BUILD_DIR)/tappet ./cmd/tappet
	$(GO_BUILD) -o $(BUILD_DIR)/tappet-structure-generator ./structure_generator/cmd

.PHONY: build-linux-amd64
build-linux-amd64:
	mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 $(GO_BUILD) -o $(BUILD_DIR)/tappet-linux-amd64 ./cmd/tappet
	GOOS=linux GOARCH=amd64 $(GO_BUILD) -o $(BUILD_DIR)/tappet-structure-generator-linux-amd64 ./structure_generator/cmd

.PHONY: test
test:
	go test ./...

.PHONY: test-race
test-race:
	go test -race ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: fmt
fmt:
	gofmt -w $$(git ls-files '*.go')

.PHONY: fmt-check
fmt-check:
	test -z "$$(gofmt -l $$(git ls-files '*.go'))"

.PHONY: check
check: fmt-check test test-race vet build

.PHONY: build-image
build-image:
	docker buildx build --platform=linux/amd64,linux/arm64 -t ghcr.io/gearboxlogic/tappet:latest . --push --provenance=false

.PHONY: test-container
test-container:
	$(CONTAINER_ENGINE) build -t tappet:smoke .
	CONTAINER_ENGINE=$(CONTAINER_ENGINE) ./script/test-container.sh tappet:smoke
