GO ?= go
BIN_DIR := bin
STANDALONE_BIN := $(BIN_DIR)/standalone
API_BIN := $(BIN_DIR)/api

.PHONY: build lint standalone api web web-build

# Release build: bundle the SPA first so go:embed picks up a fresh web/dist,
# then compile. The binaries are fully self-contained (API + web UI).
build: web-build
	$(GO) build -o $(STANDALONE_BIN) ./cmd/standalone
	$(GO) build -o $(API_BIN) ./cmd/api

# Production SPA bundle into web/dist (what go:embed ships). web/.env.production
# pins the bundle to same-origin + standalone mode; override via shell env, e.g.
# `VITE_APP_MODE=distributed make web-build`. The .gitkeep placeholder is
# restored because vite empties the output dir.
web-build:
	pnpm --dir web run build
	@touch web/dist/.gitkeep

fmt:
	$(GO) fmt ./...

lint:
	$(GO) vet ./...
	pnpm --dir web run lint

mod:
	$(GO) mod tidy

standalone:
	$(GO) run ./cmd/standalone serve

api:
	$(GO) run ./cmd/api

hub:
	$(GO) run ./cmd/hub

agent:
	$(GO) run ./cmd/agent

web:
	pnpm --dir web dev

sqlc:
	@echo "Generating SQLC code..."
	@sqlc generate

grpc:
	@echo "Installing protoc plugins..."
	@go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	@go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	@echo "Generating gRPC code..."
	@PATH="$$PATH:$$(go env GOPATH)/bin" protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative internal/infra/transport/grpc/proto/hub.proto

generate-hub-certs:
	mkdir -p data/hub-certs
	openssl ecparam -name prime256v1 -genkey -noout -out data/hub-certs/ca.key
	openssl req -x509 -new -key data/hub-certs/ca.key -sha256 -days 36500 -out data/hub-certs/ca.crt -subj "/C=CA/O=WinterFlow.io/OU=CA/CN=WinterFlow.io CA/emailAddress=info@winterflow.io"
	openssl ecparam -name prime256v1 -genkey -noout -out data/hub-certs/hub.key
	openssl req -new -key data/hub-certs/hub.key -out data/hub-certs/hub.csr -subj "/C=CA/O=WinterFlow.io/OU=SERVER/CN=winterflow.io/emailAddress=info@winterflow.io"
	openssl x509 -req -in data/hub-certs/hub.csr -CA data/hub-certs/ca.crt -CAkey data/hub-certs/ca.key -CAcreateserial -out data/hub-certs/hub.crt -days 36500 -sha256 -extfile data/hub-certs/ext.cnf -extensions v3_ext
	cat data/hub-certs/hub.crt data/hub-certs/ca.crt > data/hub-certs/hub_fullchain.crt

generate-agent-certs:
	mkdir -p data/agent-certs
	@openssl ecparam -name prime256v1 -genkey -noout -out data/agent-certs/agent.key
	@openssl req -new -key data/agent-certs/agent.key -out data/agent-certs/agent.csr -subj "/C=CA/O=WinterFlow.io/OU=CLIENT/CN=winterflow-agent"
	@openssl x509 -req -in data/agent-certs/agent.csr -CA data/hub-certs/ca.crt -CAkey data/hub-certs/ca.key -CAcreateserial -out data/agent-certs/agent.crt -days 36500 -sha256
