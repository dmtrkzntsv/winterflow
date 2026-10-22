package hubusers

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"winterflow/internal/domain/model"
	redisbus "winterflow/internal/infra/transport/redis/bus"
	hubsreg "winterflow/internal/infra/transport/redis/hubs"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// The integration tests need a real Redis with ACLs and an aclfile (make
// test-redis starts one); they are skipped without WF_TEST_REDIS_ADDR.

func adminClient(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("WF_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("WF_TEST_REDIS_ADDR not set (run make test-redis)")
	}
	rc := redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("WF_TEST_REDIS_PASSWORD")})
	t.Cleanup(func() { _ = rc.Close() })
	if err := rc.Ping(t.Context()).Err(); err != nil {
		t.Fatalf("admin ping: %v", err)
	}
	return rc
}

// hubClient is the client a hub builds (NewRedisClient from its env), logged
// in as username / password.
func hubClient(t *testing.T, username, password string) *redis.Client {
	t.Helper()
	t.Setenv("REDIS_ADDR", os.Getenv("WF_TEST_REDIS_ADDR"))
	t.Setenv("REDIS_TLS", "false")
	t.Setenv("REDIS_USERNAME", username)
	t.Setenv("REDIS_PASSWORD", password)
	t.Setenv("REDIS_DB", "")
	rc, err := redisbus.NewRedisClient(config.NewServerConfig("distributed"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rc.Close() })
	return rc
}

func ensure(t *testing.T, u *Users, hubID string) (string, string) {
	t.Helper()
	name, pw, err := u.Ensure(t.Context(), hubID)
	if err != nil {
		t.Fatalf("Ensure(%s): %v", hubID, err)
	}
	t.Cleanup(func() { _ = u.Delete(context.Background(), hubID) })
	return name, pw
}

func isNoPerm(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "NOPERM")
}

func clientCount(t *testing.T, admin *redis.Client, user string) int {
	t.Helper()
	list, err := admin.ClientList(t.Context()).Result()
	if err != nil {
		t.Fatalf("client list: %v", err)
	}
	n := 0
	for _, line := range strings.Split(list, "\n") {
		if slices.Contains(strings.Fields(line), "user="+user) {
			n++
		}
	}
	return n
}

// aclLog is the server's list of refused commands, as "reason object".
func aclLog(t *testing.T, admin *redis.Client) []string {
	t.Helper()
	entries, err := admin.ACLLog(t.Context(), 100).Result()
	if err != nil {
		t.Fatalf("acl log: %v", err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Reason+" "+e.Object+" ("+e.Username+")")
	}
	return out
}

func TestIntegrationHubUserCanDoExactlyTheHubOperations(t *testing.T) {
	admin := adminClient(t)
	u := New(admin)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	const id, other = "hub-a.wf-test.dev", "hub-b.wf-test.dev"
	name, pw := ensure(t, u, id)
	ensure(t, u, other)
	if name != "hub:"+id {
		t.Fatalf("username %q", name)
	}
	if err := admin.ACLLogReset(ctx).Err(); err != nil {
		t.Fatal(err)
	}

	// Everything the hub does, through the hub's own code paths: connect
	// (HELLO; no CLIENT SETINFO, see NewRedisClient), announce, drain flag, the bus.
	rc := hubClient(t, name, pw)
	if err := redisbus.Ping(ctx, rc); err != nil {
		t.Fatalf("ping: %v", err)
	}
	reg := hubsreg.NewRegistry(rc)
	if err := reg.Announce(ctx, model.Hub{ID: id, Accepting: true}, 90*time.Second); err != nil {
		t.Fatalf("announce: %v", err)
	}
	if ttl := admin.TTL(ctx, "hub:status:"+id).Val(); ttl <= 0 || ttl > 90*time.Second {
		t.Fatalf("status key ttl %v", ttl)
	}
	if err := reg.SetDraining(ctx, id, true); err != nil {
		t.Fatalf("set draining: %v", err)
	}
	if on, err := reg.IsDraining(ctx, id); err != nil || !on {
		t.Fatalf("is draining = %v, %v", on, err)
	}
	if v, err := rc.Get(ctx, "hub:draining:"+id).Result(); err != nil || v != "1" {
		t.Fatalf("get draining = %q, %v", v, err)
	}
	if err := reg.SetDraining(ctx, id, false); err != nil {
		t.Fatalf("undrain: %v", err)
	}
	if err := reg.Withdraw(ctx, id); err != nil {
		t.Fatalf("withdraw: %v", err)
	}

	// The hub subscribes to its own queue, its replies and the broadcast;
	// the API (admin here) publishes on them.
	subs := []string{"bus:requests:" + id, "hub:responses:" + id, "hub:broadcast"}
	ps := rc.Subscribe(ctx, subs...)
	defer ps.Close()
	for range subs {
		if _, err := ps.Receive(ctx); err != nil {
			t.Fatalf("subscribe: %v", err)
		}
	}
	for _, ch := range subs {
		if err := admin.Publish(ctx, ch, "m:"+ch).Err(); err != nil {
			t.Fatal(err)
		}
		msg, err := ps.ReceiveMessage(ctx)
		if err != nil || msg.Channel != ch || msg.Payload != "m:"+ch {
			t.Fatalf("receive on %s: %+v, %v", ch, msg, err)
		}
	}
	if err := ps.Unsubscribe(ctx, subs[0]); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	if err := ps.Ping(ctx); err != nil {
		t.Fatalf("ping while subscribed: %v", err)
	}

	// The hub publishes agent replies, agent events and its requests to the
	// API; the API (admin here) reads them.
	pubs := []string{"hub:requests", "bus:responses", "bus:events"}
	aps := admin.Subscribe(ctx, pubs...)
	defer aps.Close()
	for range pubs {
		if _, err := aps.Receive(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, ch := range pubs {
		if err := rc.Publish(ctx, ch, "m:"+ch).Err(); err != nil {
			t.Fatalf("publish %s: %v", ch, err)
		}
		msg, err := aps.ReceiveMessage(ctx)
		if err != nil || msg.Channel != ch || msg.Payload != "m:"+ch {
			t.Fatalf("receive on %s: %+v, %v", ch, msg, err)
		}
	}

	// The bus the hub runs on: a JSON message in on its reply channel, one
	// out on hub:requests.
	b := redisbus.NewBus(rc, logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}))
	msgs, stop, err := b.Subscribe(ctx, "hub:responses:"+id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stop() }()
	deadline := time.After(10 * time.Second)
	for got := false; !got; {
		_ = admin.Publish(ctx, "hub:responses:"+id, `{"k":"v"}`).Err()
		select {
		case m := <-msgs:
			got = m.Payload == `{"k":"v"}`
		case <-time.After(100 * time.Millisecond):
		case <-deadline:
			t.Fatal("no bus message")
		}
	}
	if err := b.Publish(ctx, "hub:requests", map[string]string{"k": "v"}); err != nil {
		t.Fatalf("bus publish: %v", err)
	}
	if msg, err := aps.ReceiveMessage(ctx); err != nil || msg.Payload != `{"k":"v"}` {
		t.Fatalf("bus publish received: %+v, %v", msg, err)
	}

	// A non-zero REDIS_DB makes go-redis send SELECT on connect.
	t.Setenv("REDIS_DB", "1")
	rc1, err := redisbus.NewRedisClient(config.NewServerConfig("distributed"))
	if err != nil {
		t.Fatal(err)
	}
	defer rc1.Close()
	if err := rc1.Set(ctx, "hub:status:"+id, "{}", time.Minute).Err(); err != nil {
		t.Fatalf("REDIS_DB=1: %v", err)
	}
	_ = rc1.Del(ctx, "hub:status:"+id)

	// The stopping hub's peer listing (SCAN + MGET over every hub's status)
	// works, and reads another hub's status.
	if err := admin.Set(ctx, "hub:status:"+other, `{"id":"`+other+`","accepting":true}`, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if err := reg.Announce(ctx, model.Hub{ID: id, Accepting: true}, 90*time.Second); err != nil {
		t.Fatalf("announce: %v", err)
	}
	listed, err := reg.List(ctx)
	if err != nil {
		t.Fatalf("registry List: %v", err)
	}
	ids := []string{}
	for _, h := range listed {
		ids = append(ids, h.ID)
	}
	if !slices.Contains(ids, id) || !slices.Contains(ids, other) {
		t.Fatalf("registry List = %q, want both hubs", ids)
	}
	if v, err := rc.Get(ctx, "hub:status:"+other).Result(); err != nil || v == "" {
		t.Fatalf("get other status = %q, %v", v, err)
	}

	// Nothing on the way was refused: the handshake commands go-redis sends
	// are all allowed.
	if log := aclLog(t, admin); len(log) != 0 {
		t.Fatalf("refused commands during normal hub work: %q", log)
	}

	// Refused: another hub's keys and channels, other keys, SCAN, FLUSHALL.
	refused := map[string]error{
		"get other draining":   rc.Get(ctx, "hub:draining:"+other).Err(),
		"mget other draining":  rc.MGet(ctx, "hub:status:"+other, "hub:draining:"+other).Err(),
		"mget unrelated":       rc.MGet(ctx, "session:x").Err(),
		"expire other status":  rc.Expire(ctx, "hub:status:"+other, time.Second).Err(),
		"set other status":     rc.Set(ctx, "hub:status:"+other, "x", time.Minute).Err(),
		"set other draining":   rc.Set(ctx, "hub:draining:"+other, "1", 0).Err(),
		"exists other":         rc.Exists(ctx, "hub:draining:"+other).Err(),
		"del other":            rc.Del(ctx, "hub:status:"+other).Err(),
		"get unrelated":        rc.Get(ctx, "session:x").Err(),
		"publish other queue":  rc.Publish(ctx, "bus:requests:"+other, "x").Err(),
		"publish broadcast":    rc.Publish(ctx, "hub:broadcast", "x").Err(),
		"publish own queue":    rc.Publish(ctx, "bus:requests:"+id, "x").Err(),
		"publish own replies":  rc.Publish(ctx, "hub:responses:"+id, "x").Err(),
		"publish other resp":   rc.Publish(ctx, "hub:responses:"+other, "x").Err(),
		"publish bus:requests": rc.Publish(ctx, "bus:requests", "x").Err(),
		"keys":                 rc.Keys(ctx, "*").Err(),
		"flushall":             rc.FlushAll(ctx).Err(),
		"acl whoami":           rc.Do(ctx, "ACL", "WHOAMI").Err(),
		"config get":           rc.ConfigGet(ctx, "*").Err(),
		"psubscribe":           rc.Do(ctx, "PSUBSCRIBE", "*").Err(),
	}
	for what, err := range refused {
		if !isNoPerm(err) {
			t.Errorf("%s: want NOPERM, got %v", what, err)
		}
	}
	for _, ch := range []string{
		"hub:responses:" + other, "bus:requests:" + other,
		"bus:responses", "bus:events", "hub:requests",
	} {
		ps2 := rc.Subscribe(ctx, ch)
		if _, err := ps2.Receive(ctx); !isNoPerm(err) {
			t.Errorf("subscribe %s: want NOPERM, got %v", ch, err)
		}
		_ = ps2.Close()
	}
	if v := admin.Get(ctx, "hub:status:"+other).Val(); v == "" || v == "x" {
		t.Errorf("another hub's status was changed or deleted: %q", v)
	}
}

func TestIntegrationEnsureAgainResetsThePassword(t *testing.T) {
	admin := adminClient(t)
	u := New(admin)
	ctx := t.Context()
	const id = "hub-reset.wf-test.dev"
	name, oldPW := ensure(t, u, id)
	old := hubClient(t, name, oldPW)
	if err := old.Ping(ctx).Err(); err != nil {
		t.Fatalf("old password: %v", err)
	}
	ps := old.Subscribe(ctx, "hub:broadcast")
	defer ps.Close()
	if _, err := ps.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	if n := clientCount(t, admin, name); n < 2 {
		t.Fatalf("sessions before reset: %d, want 2", n)
	}

	name2, newPW, err := u.Ensure(ctx, id)
	if err != nil || name2 != name || newPW == oldPW {
		t.Fatalf("Ensure again = %q, same password %v, %v", name2, newPW == oldPW, err)
	}
	if n := clientCount(t, admin, name); n != 0 {
		t.Fatalf("sessions after reset: %d, want 0 (old ones killed)", n)
	}
	// The old client reconnects with the old password and is refused.
	if err := old.Ping(ctx).Err(); err == nil || !strings.Contains(err.Error(), "WRONGPASS") {
		t.Fatalf("old password after reset: want WRONGPASS, got %v", err)
	}
	if err := hubClient(t, name, newPW).Ping(ctx).Err(); err != nil {
		t.Fatalf("new password: %v", err)
	}
}

func TestIntegrationDelete(t *testing.T) {
	admin := adminClient(t)
	u := New(admin)
	ctx := t.Context()
	const id = "hub-del.wf-test.dev"
	name, pw := ensure(t, u, id)
	rc := hubClient(t, name, pw)
	if err := rc.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	if err := u.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if n := clientCount(t, admin, name); n != 0 {
		t.Fatalf("sessions after delete: %d", n)
	}
	if users, _ := admin.Do(ctx, "ACL", "USERS").StringSlice(); slices.Contains(users, name) {
		t.Fatalf("user still listed: %q", users)
	}
	if err := rc.Ping(ctx).Err(); err == nil {
		t.Fatal("deleted user still connects")
	}
	if err := u.Delete(ctx, id); err != nil {
		t.Fatalf("Delete of a missing user: %v", err)
	}
	if err := u.Delete(ctx, "never-existed.wf-test.dev"); err != nil {
		t.Fatalf("Delete of a never-created user: %v", err)
	}
}

func TestIntegrationUsersSurviveACLLoad(t *testing.T) {
	admin := adminClient(t)
	u := New(admin)
	ctx := t.Context()
	const id, gone = "hub-load.wf-test.dev", "hub-gone.wf-test.dev"
	name, pw := ensure(t, u, id)
	ensure(t, u, gone)
	if err := u.Delete(ctx, gone); err != nil {
		t.Fatal(err)
	}
	if err := admin.Do(ctx, "ACL", "LOAD").Err(); err != nil {
		t.Fatalf("acl load: %v", err)
	}
	users, err := admin.Do(ctx, "ACL", "USERS").StringSlice()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(users, name) || slices.Contains(users, Username(gone)) {
		t.Fatalf("users after ACL LOAD: %q", users)
	}
	rc := hubClient(t, name, pw)
	if err := rc.Set(ctx, "hub:status:"+id, "{}", time.Minute).Err(); err != nil {
		t.Fatalf("after ACL LOAD: %v", err)
	}
	if err := rc.Set(ctx, "hub:status:other", "{}", time.Minute).Err(); !isNoPerm(err) {
		t.Fatalf("rules after ACL LOAD: write another hub's status: want NOPERM, got %v", err)
	}
	if err := rc.Get(ctx, "hub:draining:other").Err(); !isNoPerm(err) {
		t.Fatalf("rules after ACL LOAD: read another hub's drain flag: want NOPERM, got %v", err)
	}
	if err := rc.Get(ctx, "hub:status:other").Err(); err != nil && !errors.Is(err, redis.Nil) {
		t.Fatalf("rules after ACL LOAD: read another hub's status: %v", err)
	}
}

func TestIntegrationNoACLFileIsAConfigurationError(t *testing.T) {
	admin := adminClient(t)
	// The test server has an aclfile; check that ACL SAVE reaches it and
	// that the classification of the no-aclfile reply holds on this server's
	// wording by asking a server without one, when the caller provides it.
	addr := os.Getenv("WF_TEST_REDIS_NOACL_ADDR")
	if addr == "" {
		if err := New(admin).save(t.Context()); err != nil {
			t.Fatalf("acl save with an aclfile: %v", err)
		}
		t.Skip("WF_TEST_REDIS_NOACL_ADDR not set")
	}
	rc := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rc.Close() })
	u := New(rc)
	_, _, err := u.Ensure(t.Context(), "hub-noacl.wf-test.dev")
	if !errors.Is(err, ErrNoACLFile) {
		t.Fatalf("Ensure without an aclfile: want ErrNoACLFile, got %v", err)
	}
	_ = u.Delete(context.Background(), "hub-noacl.wf-test.dev")
}
