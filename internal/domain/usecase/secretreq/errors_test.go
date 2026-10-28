package secretreq_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/usecase/secretreq"
	"winterflow/pkg/crypto/cryptotest"
)

var errDB = errors.New("database is gone")

// broken fails the methods named in fail with errDB, and answers false
// (without an error) for those named in lose: a write another caller won.
type broken struct {
	port.SecretRequestRepository
	fail, lose map[string]bool
}

func (b *broken) Create(ctx context.Context, r model.SecretRequest, h string) error {
	if b.fail["Create"] {
		return errDB
	}
	return b.SecretRequestRepository.Create(ctx, r, h)
}

func (b *broken) GetByTokenHash(ctx context.Context, h string) (model.SecretRequest, error) {
	if b.fail["GetByTokenHash"] {
		return model.SecretRequest{}, errDB
	}
	return b.SecretRequestRepository.GetByTokenHash(ctx, h)
}

func (b *broken) Get(ctx context.Context, orgID, id string) (model.SecretRequest, error) {
	if b.fail["Get"] {
		return model.SecretRequest{}, errDB
	}
	return b.SecretRequestRepository.Get(ctx, orgID, id)
}

func (b *broken) Submit(ctx context.Context, id string, cts map[string]string, now, exp time.Time) (bool, error) {
	switch {
	case b.fail["Submit"]:
		return false, errDB
	case b.lose["Submit"]:
		return false, nil
	}
	return b.SecretRequestRepository.Submit(ctx, id, cts, now, exp)
}

func (b *broken) Claim(ctx context.Context, id, cid string, now, until time.Time) (bool, error) {
	switch {
	case b.fail["Claim"]:
		return false, errDB
	case b.lose["Claim"]:
		return false, nil
	}
	return b.SecretRequestRepository.Claim(ctx, id, cid, now, until)
}

func (b *broken) MarkDispatched(ctx context.Context, id, cid string, until time.Time) (bool, error) {
	if b.fail["MarkDispatched"] {
		return false, errDB
	}
	return b.SecretRequestRepository.MarkDispatched(ctx, id, cid, until)
}

func (b *broken) Expire(ctx context.Context, now time.Time) (int64, error) {
	if b.fail["Expire"] {
		return 0, errDB
	}
	return b.SecretRequestRepository.Expire(ctx, now)
}

func (b *broken) Prune(ctx context.Context, cutoff time.Time) (int64, error) {
	if b.fail["Prune"] {
		return 0, errDB
	}
	return b.SecretRequestRepository.Prune(ctx, cutoff)
}

// setupBroken is setup over a repository that can be broken per method.
func setupBroken(t *testing.T) (*secretreq.UseCase, *broken) {
	t.Helper()
	b := &broken{fail: map[string]bool{}, lose: map[string]bool{}}
	u, _, _ := setupWith(t, func(r port.SecretRequestRepository) port.SecretRequestRepository {
		b.SecretRequestRepository = r
		return b
	})
	return u, b
}

// submitted is alice's request for API_KEY, submitted.
func submitted(t *testing.T, u *secretreq.UseCase) secretreq.Request {
	t.Helper()
	req, raw := request(t, u, false, "API_KEY")
	if err := u.Submit(context.Background(), alice, raw, map[string]string{"API_KEY": cryptotest.Ciphertext(t, "v")}); err != nil {
		t.Fatal(err)
	}
	return req
}

// TestDatabaseErrorsSurface: a repository error is returned as is (never
// read as "not found", which would tell the agent to ask again).
func TestDatabaseErrorsSurface(t *testing.T) {
	u, b := setupBroken(t)
	ctx := context.Background()
	req, raw := request(t, u, false, "API_KEY")
	ct := map[string]string{"API_KEY": cryptotest.Ciphertext(t, "v")}

	for _, c := range []struct {
		method string
		run    func() error
	}{
		{"Create", func() error {
			_, _, err := u.Create(ctx, alice, serverA, nil, []string{"API_KEY"}, nil, "")
			return err
		}},
		{"Get", func() error { _, err := u.Get(ctx, alice, req.ID); return err }},
		{"GetByTokenHash", func() error { _, err := u.View(ctx, alice, raw); return err }},
		{"Submit", func() error { return u.Submit(ctx, alice, raw, ct) }},
		{"Get", func() error { _, _, err := u.Consume(ctx, alice, serverA, "", req.ID); return err }},
		{"Expire", func() error { return u.Sweep(ctx) }},
		{"Prune", func() error { return u.Sweep(ctx) }},
	} {
		b.fail[c.method] = true
		if err := c.run(); !errors.Is(err, errDB) {
			t.Errorf("%s failing: %v, want the database error", c.method, err)
		}
		b.fail[c.method] = false
	}

	if err := u.Submit(ctx, alice, raw, ct); err != nil {
		t.Fatal(err)
	}
	b.fail["Claim"] = true
	if _, _, err := u.Consume(ctx, alice, serverA, "", req.ID); !errors.Is(err, errDB) {
		t.Fatalf("Claim failing: %v", err)
	}
	b.fail["Claim"] = false
	_, claim, err := u.Consume(ctx, alice, serverA, "", req.ID)
	if err != nil {
		t.Fatal(err)
	}
	b.fail["MarkDispatched"] = true
	if err := claim.Dispatch(ctx); !errors.Is(err, errDB) {
		t.Fatalf("MarkDispatched failing: %v", err)
	}
	if err := claim.Commit(false); err != nil {
		t.Fatal(err)
	}
}

// TestLostRaces: a write another caller won reads as the state it left:
// a submission as an invalid link, a claim as a request in use.
func TestLostRaces(t *testing.T) {
	u, b := setupBroken(t)
	ctx := context.Background()
	_, raw := request(t, u, false, "API_KEY")
	b.lose["Submit"] = true
	if err := u.Submit(ctx, alice, raw, map[string]string{"API_KEY": cryptotest.Ciphertext(t, "v")}); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("submit that lost = %v", err)
	}
	b.lose["Submit"] = false
	req := submitted(t, u)
	b.lose["Claim"] = true
	if _, _, err := u.Consume(ctx, alice, serverA, "", req.ID); err == nil || !strings.Contains(err.Error(), "already being used") {
		t.Fatalf("claim that lost = %v", err)
	}
}

// TestCallerErrors: the caller's org or the ownership lookup failing stops
// every operation; an unknown token or a malformed id reads not found.
func TestCallerErrors(t *testing.T) {
	u, _ := setup(t)
	ctx := context.Background()
	req, raw := request(t, u, false, "API_KEY")
	const carol = "carol" // in no org

	// Ownership is checked first: carol owns nothing.
	if _, _, err := u.Create(ctx, carol, serverA, nil, []string{"API_KEY"}, nil, ""); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("create by a non-owner = %v", err)
	}
	if _, err := u.Get(ctx, carol, req.ID); err == nil || errors.Is(err, model.ErrNotFound) {
		t.Fatalf("get without an org = %v", err)
	}
	if _, err := u.View(ctx, carol, raw); err == nil || errors.Is(err, model.ErrNotFound) {
		t.Fatalf("view without an org = %v", err)
	}
	if _, _, err := u.Consume(ctx, carol, serverA, "", req.ID); err == nil || errors.Is(err, model.ErrNotFound) {
		t.Fatalf("consume without an org = %v", err)
	}
	if _, err := u.Get(ctx, alice, "not-a-uuid"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("get of a malformed id = %v", err)
	}
	if _, err := u.Get(ctx, alice, strings.ToUpper(req.ID)); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("get of a non-canonical id = %v", err)
	}
	for _, tok := range []string{"", "wfp_" + raw[4:], raw + strings.Repeat("x", 128)} {
		if _, err := u.View(ctx, alice, tok); !errors.Is(err, model.ErrNotFound) {
			t.Errorf("view of %q = %v", tok, err)
		}
	}
}

// TestCreateErrors: a lookup error is returned; an over-long name refused.
func TestCreateErrors(t *testing.T) {
	u, _ := setup(t)
	ctx := context.Background()
	long := "A" + strings.Repeat("B", secretreq.MaxNameBytes)
	if _, _, err := u.Create(ctx, alice, serverA, nil, []string{long}, nil, ""); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("long name = %v", err)
	}
	failing := secretreq.New(secretreq.Deps{Orgs: orgs{}, Servers: ownsAll{}})
	if _, _, err := failing.Create(ctx, alice, serverA, nil, []string{"API_KEY"}, nil, ""); err == nil || !strings.Contains(err.Error(), "no org") {
		t.Fatalf("create without an org = %v", err)
	}
	failing = secretreq.New(secretreq.Deps{Orgs: orgs{alice: orgA}, Servers: errServers{}})
	if _, _, err := failing.Create(ctx, alice, serverA, nil, []string{"API_KEY"}, nil, ""); !errors.Is(err, errDB) {
		t.Fatalf("create with a failing ownership check = %v", err)
	}
}

// TestSubmitCapsCiphertext: a value over MaxCiphertextBytes is refused
// before its shape is even checked.
func TestSubmitCapsCiphertext(t *testing.T) {
	u, _ := setup(t)
	_, raw := request(t, u, false, "API_KEY")
	big := strings.Repeat("A", secretreq.MaxCiphertextBytes+1)
	if err := u.Submit(context.Background(), alice, raw, map[string]string{"API_KEY": big}); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized value = %v", err)
	}
}

type ownsAll struct{}

func (ownsAll) UserOwnsServer(context.Context, string, string) (bool, error) { return true, nil }
func (ownsAll) GetServers(context.Context, string) ([]model.Server, error)   { return nil, nil }

type errServers struct{}

func (errServers) UserOwnsServer(context.Context, string, string) (bool, error) { return false, errDB }
func (errServers) GetServers(context.Context, string) ([]model.Server, error)   { return nil, errDB }
