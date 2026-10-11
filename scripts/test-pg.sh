#!/usr/bin/env bash
# Run the API contract suite and the internal/infra/db packages (migration
# runner, repositories) against a throwaway postgres:16 container on a random
# host port (make test-pg). The container is removed on exit, pass or fail.
# Extra arguments go to `go test` (e.g. -run TestParity -v).
set -euo pipefail

GO="${GO:-go}"
cid="$(docker run -d --rm -p 127.0.0.1:0:5432 -e POSTGRES_PASSWORD=postgres postgres:16 -c max_connections=200)"
trap 'docker rm -f "$cid" >/dev/null 2>&1 || true' EXIT

port="$(docker port "$cid" 5432/tcp | head -n1 | sed 's/.*://')"
for _ in $(seq 1 60); do
	if docker exec "$cid" pg_isready -U postgres -h 127.0.0.1 >/dev/null 2>&1; then
		break
	fi
	sleep 0.5
done
docker exec "$cid" pg_isready -U postgres -h 127.0.0.1 >/dev/null

export WF_TEST_DATABASE_URL="postgres://postgres:postgres@127.0.0.1:${port}/postgres?sslmode=disable"
"$GO" test -count=1 "$@" ./internal/app/web/contract/ ./internal/infra/db/...
