#!/usr/bin/env bash
# Run the Redis packages' tests and the hub's (the ACL integration tests
# included: the hub running on its own ACL user) against a throwaway redis:8
# container on a random host port (make test-redis). The server runs with a
# password for the default user and an aclfile, like the control plane's
# dedicated instance (TLS off: the ACL logic does not depend on it). The
# container is removed on exit, pass or fail. Extra arguments go to `go test`
# (e.g. -run TestIntegration -v).
set -euo pipefail

GO="${GO:-go}"
password="test-$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')"

# The aclfile must exist before redis-server starts, and it must define the
# default user with the password: users loaded from an aclfile replace
# requirepass, and a file without a default user leaves it with nopass (the
# control plane installer writes the same line). It lives inside the
# container so the server can write it (ACL SAVE) as its own user. The
# password is a throwaway, passed through the environment.
password_hash="$(printf '%s' "$password" | sha256sum | cut -d' ' -f1)"
cid="$(docker run -d --rm -p 127.0.0.1:0:6379 -e WF_PW="$password" -e WF_PW_HASH="$password_hash" --entrypoint sh redis:8 -c \
	'printf "user default on sanitize-payload #%s ~* &* +@all\n" "$WF_PW_HASH" > /data/users.acl && exec redis-server --port 6379 --aclfile /data/users.acl --requirepass "$WF_PW"')"
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
"$GO" test -count=1 "$@" ./internal/infra/transport/redis/... ./internal/infra/transport/grpc/hub/
