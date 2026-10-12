GO ?= go
BIN_DIR := bin
STANDALONE_BIN := $(BIN_DIR)/standalone
API_BIN := $(BIN_DIR)/api

.PHONY: build lint standalone api web web-build third-party-licenses test web-test cover diffcov openapi test-pg sqlc sqlc-check

# Release build: bundle the SPA first so go:embed picks up a fresh web/dist,
# then compile. The binaries are fully self-contained (API + web UI) and
# static (CGO_ENABLED=0): the backup helper runs the binary inside alpine,
# which has no glibc loader.
build: web-build
	CGO_ENABLED=0 $(GO) build -o $(STANDALONE_BIN) ./cmd/standalone
	CGO_ENABLED=0 $(GO) build -o $(API_BIN) ./cmd/api

# Production SPA bundle into web/dist (what go:embed ships). web/.env.production
# pins the bundle to same-origin + standalone mode; override via shell env, e.g.
# `VITE_APP_MODE=distributed make web-build`. The .gitkeep placeholder is
# restored because vite empties the output dir.
web-build:
	pnpm --dir web run build
	@touch web/dist/.gitkeep

# Attribution notices for every Go module and bundled npm package; goreleaser
# regenerates and attaches this to each release (see .goreleaser.yaml).
third-party-licenses:
	bash scripts/third_party_licenses.sh

fmt:
	$(GO) fmt ./...

lint:
	$(GO) vet ./...
	pnpm --dir web run lint

# Go tests exactly as CI runs them; coverage.out feeds `make cover` and
# `make diffcov`. -coverpkg=./... lets the API contract suite
# (internal/app/web/contract, test-only) count toward the code it drives.
test:
	$(GO) test -race -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./...

# The API contract suite and the internal/infra/db packages against a
# throwaway postgres:16 container (Docker). Plain `go test ./...` stays on
# SQLite. Extra go test flags:
# make test-pg TEST_PG_ARGS='-run TestParity -v'
test-pg:
	GO=$(GO) bash scripts/test-pg.sh $(TEST_PG_ARGS)

# Web unit tests (Vitest) with coverage into web/coverage/lcov.info.
web-test:
	pnpm --dir web run test:coverage

# Total Go and web coverage against coverage-baseline.json (CI's ratchet).
cover: test web-test
	$(GO) run ./tools/diffcov ratchet --go coverage.out --lcov web/coverage/lcov.info

# CI's diff-coverage gate: lines changed since $(DIFFCOV_BASE) must be at
# least 80% covered, per language. `git fetch origin main` first if it is
# stale; override the base for stacked branches (make diffcov DIFFCOV_BASE=...).
DIFFCOV_BASE ?= origin/main
diffcov: test web-test
	$(GO) run ./tools/diffcov --base $(DIFFCOV_BASE) --min 80 --go coverage.out --lcov web/coverage/lcov.info

# Regenerate api/openapi.yaml from the route tables in internal/app/web.
# TestOpenAPIUpToDate fails until the committed file matches.
openapi:
	$(GO) run ./cmd/openapi > api/openapi.yaml.tmp || { rm -f api/openapi.yaml.tmp; exit 1; }
	mv api/openapi.yaml.tmp api/openapi.yaml

mod:
	$(GO) mod tidy

standalone:
	CGO_ENABLED=0 $(GO) run ./cmd/standalone serve

api:
	CGO_ENABLED=0 $(GO) run ./cmd/api

hub:
	CGO_ENABLED=0 $(GO) run ./cmd/hub

agent:
	CGO_ENABLED=0 $(GO) run ./cmd/agent

web:
	pnpm --dir web dev

# sqlc is pinned and run through `go run`, so nothing needs installing (its
# Postgres parser is cgo: a C compiler is required; the first run takes about
# a minute). make sqlc regenerates internal/infra/db/sqlc/gen; make sqlc-check
# fails when the committed code is stale (CI).
SQLC_VERSION := v1.31.1
SQLC := $(GO) run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION) -f internal/infra/db/sqlc/sqlc.yaml

sqlc:
	$(SQLC) generate

sqlc-check:
	$(SQLC) diff

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
