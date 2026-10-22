#!/usr/bin/env bash
# Run the Redis packages' tests, the ACL integration tests included, against a
# throwaway redis:8 container on a random host port (make test-redis). The
# server runs with a password for the default user and an aclfile, like the
# control plane's dedicated instance (TLS off: the ACL logic does not depend
# on it). The container is removed on exit, pass or fail. Extra arguments go
# to `go test` (e.g. -run TestIntegration -v).
set -euo pipefail

GO="${GO:-go}"
password="test-$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')"

# The aclfile must exist (empty is fine) before redis-server starts; it lives
# inside the container so the server can write it (ACL SAVE) as its own user.
# The password is a throwaway, passed through the environment.
cid="$(docker run -d --rm -p 127.0.0.1:0:6379 -e WF_PW="$password" --entrypoint sh redis:8 -c \
	'touch /data/users.acl && exec redis-server --port 6379 --aclfile /data/users.acl --requirepass "$WF_PW"')"
# A second server without an aclfile (and without a password): ACL SAVE there
# must surface as a configuration error.
noacl="$(docker run -d --rm -p 127.0.0.1:0:6379 redis:8 redis-server --port 6379 --protected-mode no)"
trap 'docker rm -f "$cid" "$noacl" >/dev/null 2>&1 || true' EXIT

ready() {
	docker exec -e REDISCLI_AUTH="$password" "$cid" redis-cli ping 2>/dev/null | grep -q PONG &&
		docker exec "$noacl" redis-cli ping 2>/dev/null | grep -q PONG
}
for _ in $(seq 1 60); do
	if ready; then
		break
	fi
	sleep 0.5
done
ready

hostport() { docker port "$1" 6379/tcp | head -n1 | sed 's/.*://'; }
WF_TEST_REDIS_ADDR="127.0.0.1:$(hostport "$cid")"
WF_TEST_REDIS_NOACL_ADDR="127.0.0.1:$(hostport "$noacl")"
WF_TEST_REDIS_PASSWORD="$password"
export WF_TEST_REDIS_ADDR WF_TEST_REDIS_NOACL_ADDR WF_TEST_REDIS_PASSWORD
"$GO" test -count=1 "$@" ./internal/infra/transport/redis/...
