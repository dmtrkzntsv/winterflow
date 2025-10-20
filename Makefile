GO ?= go
BIN_DIR := bin
STANDALONE_BIN := $(BIN_DIR)/standalone
API_BIN := $(BIN_DIR)/api

.PHONY: build lint standalone api

build:
	$(GO) build -o $(STANDALONE_BIN) ./cmd/standalone
	$(GO) build -o $(API_BIN) ./cmd/api

fmt:
	$(GO) fmt ./...

lint:
	$(GO) vet ./...

mod:
	$(GO) mod tidy

standalone:
	$(GO) run ./cmd/standalone

api:
	$(GO) run ./cmd/api

sqlc:
	@echo "Generating SQLC code..."
	@sqlc generate