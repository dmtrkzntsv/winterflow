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

hub:
	$(GO) run ./cmd/hub

sqlc:
	@echo "Generating SQLC code..."
	@sqlc generate

grpc:
	@echo "Installing protoc plugins..."
	@go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	@go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	@echo "Generating gRPC code..."
	@PATH="$$PATH:$$(go env GOPATH)/bin" protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative internal/infra/transport/grpc/proto/hub.proto