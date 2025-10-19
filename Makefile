GO ?= go
BIN_DIR := bin
STANDALONE_BIN := $(BIN_DIR)/standalone
API_BIN := $(BIN_DIR)/api

.PHONY: build lint standalone api

build: $(STANDALONE_BIN) $(API_BIN)

fmt:
	$(GO) fmt ./...

lint:
	$(GO) vet ./...

mod:
	$(GO) mod tidy

$(BIN_DIR):
	@mkdir -p $(BIN_DIR)

$(STANDALONE_BIN): | $(BIN_DIR)
	$(GO) build -o $@ ./cmd/standalone

$(API_BIN): | $(BIN_DIR)
	$(GO) build -o $@ ./cmd/api


standalone:
	$(GO) run ./cmd/standalone

api:
	$(GO) run ./cmd/api
