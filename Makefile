GO ?= go
BIN_DIR := bin
STANDALONE_BIN := $(BIN_DIR)/standalone
API_BIN := $(BIN_DIR)/api

.PHONY: build lint standalone api web web-build ship-dist ship-publish third-party-licenses test web-test cover diffcov openapi test-pg test-redis sqlc sqlc-check

# Release build: bundle the SPA first so go:embed picks up a fresh web/dist,
# then compile. The binaries are fully self-contained (API + web UI) and
# static (CGO_ENABLED=0): the backup helper runs the binary inside alpine,
# which has no glibc loader.
build: web-build
	CGO_ENABLED=0 $(GO) build -o $(STANDALONE_BIN) ./cmd/standalone
	CGO_ENABLED=0 $(GO) build -o $(API_BIN) ./cmd/api
	CGO_ENABLED=0 $(GO) build -o $(BIN_DIR)/hub ./cmd/hub
	CGO_ENABLED=0 $(GO) build -o $(BIN_DIR)/agent ./cmd/agent

# Production SPA bundle into web/dist (what go:embed ships). web/.env.production
# pins the bundle to same-origin + standalone mode; override via shell env, e.g.
# `VITE_APP_MODE=distributed make web-build`. The .gitkeep placeholder is
# restored because vite empties the output dir.
web-build:
	pnpm --dir web run build
	@touch web/dist/.gitkeep

# Publish folder for ship (https://github.com/dmtrkzntsv/ship), what
# `make ship-publish` uploads to R2 and `ship install` puts on hosts:
# dist/ship/winterflow-linux-{amd64,arm64,arm} (standalone, ref winterflow)
# and winterflow-{agent,api,hub}-linux-{amd64,arm64,arm} (refs
# winterflow/agent|api|hub), plus each asset's unit template and env example
# from deploy/ship. ship writes SHA256SUMS and release.json itself. The
# standalone binary embeds the standalone SPA (web/.env.production), the api
# binary the distributed one, so the web is built twice in that order; hub
# and agent embed nothing. Same flags as .goreleaser.yaml: static, -trimpath,
# GOARM=6, version through ldflags (without the leading v, like goreleaser's
# {{ .Version }}).
#
# dist/ itself is goreleaser's output folder and `goreleaser release --clean`
# wipes it, dist/ship included: build ship-dist after goreleaser (the release
# workflow does), never before. ship-dist only ever removes dist/ship.
#
# VERSION is the release tag (vYYMMDD.N); empty or dev builds stamp the
# binaries with `git describe`.
VERSION ?=
BUILD_VERSION = $(patsubst v%,%,$(if $(filter-out dev,$(VERSION)),$(VERSION),$(shell git describe --tags --always 2>/dev/null)))
RELEASE_LDFLAGS = -s -w -X winterflow/pkg/version.version=$(BUILD_VERSION)
RELEASE_ARCHES = amd64 arm64 arm
SHIP_DIST = dist/ship
SHIP_FILES = deploy/ship
# The ship asset of a cmd/ directory: the standalone is the project itself
# (ref winterflow), the rest are components (refs winterflow/agent, ...).
release_asset = $(if $(filter standalone,$(1)),winterflow,winterflow-$(1))

define NL


endef
release_build = CGO_ENABLED=0 GOOS=linux GOARCH=$(2) $(if $(filter arm,$(2)),GOARM=6) $(GO) build -trimpath -ldflags "$(RELEASE_LDFLAGS)" -o $(SHIP_DIST)/$(call release_asset,$(1))-linux-$(2) ./cmd/$(1)$(NL)

ship-dist:
	rm -rf $(SHIP_DIST) && mkdir -p $(SHIP_DIST)
	pnpm --dir web install --frozen-lockfile
	pnpm --dir web run build
	@touch web/dist/.gitkeep
	$(foreach c,standalone agent hub,$(foreach a,$(RELEASE_ARCHES),$(call release_build,$(c),$(a))))
	VITE_APP_MODE=distributed pnpm --dir web run build
	@touch web/dist/.gitkeep
	$(foreach a,$(RELEASE_ARCHES),$(call release_build,api,$(a)))
	cp $(SHIP_FILES)/*.service $(SHIP_FILES)/*.env.example $(SHIP_DIST)/

# Publish $(SHIP_DIST) with ship (it does not build: run ship-dist first, as
# CI does). VERSION=vYYMMDD.N makes an immutable version
# (s3://<bucket>/winterflow/vYYMMDD.N/; dev or empty publishes none);
# CHANNELS moves channels (latest, dev). At least one of them is required.
# Needs the ship CLI and its R2 settings: SHIP_R2_ENDPOINT,
# SHIP_R2_ACCESS_KEY_ID, SHIP_R2_SECRET_ACCESS_KEY (Read & Write token), or
# ~/.config/ship/config.toml; the bucket defaults to lab.
SHIP_VERSION_ARG = $(if $(filter-out dev,$(VERSION)),--version $(VERSION))
SHIP_CHANNELS_ARG = $(if $(CHANNELS),--channels $(CHANNELS))

ship-publish:
	@test -n "$(strip $(SHIP_VERSION_ARG) $(SHIP_CHANNELS_ARG))" || { echo "ship-publish: set VERSION (vYYMMDD.N) and/or CHANNELS (latest, dev)" >&2; exit 1; }
	ship publish winterflow --path $(SHIP_DIST) $(SHIP_VERSION_ARG) $(SHIP_CHANNELS_ARG)

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

# The Redis packages, the per-hub ACL user integration tests included,
# against a throwaway redis:8 container with an aclfile (Docker; another
# image: REDIS_IMAGE=redis:7.0 make test-redis). Plain
# `go test ./...` skips the integration tests. Extra go test flags:
# make test-redis TEST_REDIS_ARGS='-run TestIntegration -v'
test-redis:
	GO=$(GO) bash scripts/test-redis.sh $(TEST_REDIS_ARGS)

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

# Local mTLS material: the CA and a hub certificate for localhost, then an
# agent certificate (CN = SERVER_ID, signed by that CA) plus ca.crt.
generate-hub-certs:
	go run ./cmd/hub pki --dir data/hub-certs --host localhost

generate-agent-certs:
	@test -n "$(SERVER_ID)" || { echo "usage: make generate-agent-certs SERVER_ID=<server id>"; exit 2; }
	go run ./cmd/hub dev-agent-cert --dir data/hub-certs --out data/agent-certs --server-id "$(SERVER_ID)"
