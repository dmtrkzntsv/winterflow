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

generate-certs:
	mkdir -p data/certs
	openssl ecparam -name prime256v1 -genkey -noout -out data/certs/ca.key
	openssl req -x509 -new -key data/certs/ca.key -sha256 -days 36500 -out data/certs/ca.crt -subj "/C=CA/O=WinterFlow.io/OU=CA/CN=WinterFlow.io CA/emailAddress=info@winterflow.io"
	openssl ecparam -name prime256v1 -genkey -noout -out data/certs/hub.key
	openssl req -new -key data/certs/hub.key -out data/certs/hub.csr -subj "/C=CA/O=WinterFlow.io/OU=SERVER/CN=winterflow.io/emailAddress=info@winterflow.io"
	openssl x509 -req -in data/certs/hub.csr -CA data/certs/ca.crt -CAkey data/certs/ca.key -CAcreateserial -out data/certs/hub.crt -days 36500 -sha256 -extfile data/certs/ext.cnf -extensions v3_ext
	cat data/certs/hub.crt data/certs/ca.crt > data/certs/hub_fullchain.crt