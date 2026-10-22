GO ?= go
BIN_DIR := bin
STANDALONE_BIN := $(BIN_DIR)/standalone
API_BIN := $(BIN_DIR)/api

.PHONY: build lint standalone api web web-build release-binaries cdn-upload third-party-licenses test web-test cover diffcov openapi test-pg test-redis sqlc sqlc-check

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

# Release binaries for the R2 mirror (what the install scripts download):
# release-out/winterflow-{standalone,agent,api,hub}-linux-{amd64,arm64,arm}
# plus SHA256SUMS. The standalone binary embeds the standalone SPA
# (web/.env.production), the api binary the distributed one, so the web is
# built twice in that order; hub and agent embed nothing. Same flags as
# .goreleaser.yaml: static, -trimpath, GOARM=6, version through ldflags
# (VERSION without the leading v, like goreleaser's {{ .Version }}).
VERSION ?= $(shell git describe --tags --always 2>/dev/null | sed 's/^v//')
RELEASE_LDFLAGS = -s -w -X winterflow/pkg/version.version=$(VERSION)
RELEASE_ARCHES = amd64 arm64 arm
RELEASE_OUT = release-out
RELEASE_FILES = $(foreach c,standalone agent api hub,$(foreach a,$(RELEASE_ARCHES),winterflow-$(c)-linux-$(a)))

define NL


endef
release_build = CGO_ENABLED=0 GOOS=linux GOARCH=$(2) $(if $(filter arm,$(2)),GOARM=6) $(GO) build -trimpath -ldflags "$(RELEASE_LDFLAGS)" -o $(RELEASE_OUT)/winterflow-$(1)-linux-$(2) ./cmd/$(1)$(NL)

release-binaries:
	rm -rf $(RELEASE_OUT) && mkdir -p $(RELEASE_OUT)
	pnpm --dir web install --frozen-lockfile
	pnpm --dir web run build
	@touch web/dist/.gitkeep
	$(foreach c,standalone agent hub,$(foreach a,$(RELEASE_ARCHES),$(call release_build,$(c),$(a))))
	VITE_APP_MODE=distributed pnpm --dir web run build
	@touch web/dist/.gitkeep
	$(foreach a,$(RELEASE_ARCHES),$(call release_build,api,$(a)))
	cd $(RELEASE_OUT) && sha256sum $(RELEASE_FILES) > SHA256SUMS

# Mirror $(CDN_SRC) to the shared R2 bucket under <project>/<channel>/ (the
# econumo bucket; objects live under winterflow/). CHANNEL is a version tag
# (immutable cache) or latest (no-cache). Needs WINTERFLOW_R2_ENDPOINT
# (https://<account_id>.r2.cloudflarestorage.com) and the aws CLI;
# WINTERFLOW_R2_ACCESS_KEY_ID / _SECRET_ACCESS_KEY map to AWS_*.
R2_BUCKET ?= econumo
R2_PROJECT ?= winterflow
CDN_SRC ?= $(RELEASE_OUT)

cdn-upload:
	@test -n "$(CHANNEL)" || { echo "cdn-upload: set CHANNEL (latest|vYYMMDD.N)"; exit 1; }
	@test -n "$$WINTERFLOW_R2_ENDPOINT" || { echo "cdn-upload: WINTERFLOW_R2_ENDPOINT is required (https://<account_id>.r2.cloudflarestorage.com)"; exit 1; }
	@if [ -n "$$WINTERFLOW_R2_ACCESS_KEY_ID" ] && [ -n "$$WINTERFLOW_R2_SECRET_ACCESS_KEY" ]; then \
		export AWS_ACCESS_KEY_ID="$$WINTERFLOW_R2_ACCESS_KEY_ID" AWS_SECRET_ACCESS_KEY="$$WINTERFLOW_R2_SECRET_ACCESS_KEY"; \
	fi; \
	export AWS_REQUEST_CHECKSUM_CALCULATION=when_required AWS_RESPONSE_CHECKSUM_VALIDATION=when_required; \
	case "$(CHANNEL)" in \
		latest) cache="no-cache" ;; \
		*) cache="public, max-age=31536000, immutable" ;; \
	esac; \
	dest="s3://$(R2_BUCKET)/$(R2_PROJECT)/$(CHANNEL)"; \
	n=0; \
	for p in "$(CDN_SRC)"/*; do \
		[ -f "$$p" ] || continue; \
		f="$$(basename "$$p")"; \
		echo "Uploading $$f -> $$dest/$$f"; \
		aws s3 cp "$$p" "$$dest/$$f" \
			--endpoint-url "$$WINTERFLOW_R2_ENDPOINT" \
			--content-type application/octet-stream \
			--cache-control "$$cache" || exit 1; \
		n=$$((n+1)); \
	done; \
	test "$$n" -gt 0 || { echo "cdn-upload: nothing to upload in $(CDN_SRC)"; exit 1; }; \
	echo "Uploaded channel '$(CHANNEL)' to $$dest/"

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
# against a throwaway redis:8 container with an aclfile (Docker). Plain
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
