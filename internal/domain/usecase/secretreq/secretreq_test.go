package secretreq_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/usecase/secretreq"
	"winterflow/internal/infra/db/dbtest"
	secretreqrepo "winterflow/internal/infra/db/repository/secretreq"
	"winterflow/pkg/crypto/cryptotest"
)

var (
	orgA, orgB       = dbtest.ID(1), dbtest.ID(2)
	serverA, serverB = dbtest.ID(11), dbtest.ID(12)
	alice, bob       = dbtest.ID(21), dbtest.ID(22)
)

// orgs maps users to their org.
type orgs map[string]string

func (o orgs) PrimaryOrganizationID(_ context.Context, userID string) (string, error) {
	if org, ok := o[userID]; ok {
		return org, nil
	}
	return "", errors.New("no org")
}

// servers: each user owns their org's one server.
type servers struct{}

func (servers) UserOwnsServer(_ context.Context, userID, serverID string) (bool, error) {
	return (userID == alice && serverID == serverA) || (userID == bob && serverID == serverB), nil
}

func (servers) GetServers(_ context.Context, userID string) ([]model.Server, error) {
	if userID == alice {
		return []model.Server{{ID: serverA, Name: "homelab"}}, nil
	}
	return []model.Server{{ID: serverB, Name: "other"}}, nil
}

type apps struct{}

func (apps) GetApp(_ context.Context, appID string) (model.App, error) {
	if appID == "app-1" {
		return model.App{ID: appID, ServerID: serverA, Name: "Paperless"}, nil
	}
	return model.App{}, model.ErrAppNotFound
}

// clock is a settable now.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func setup(t *testing.T) (*secretreq.UseCase, *clock) {
	t.Helper()
	u, c, _ := setupWith(t, nil)
	return u, c
}

// setupWith is setup with the repository wrapped by wrap (when set); it
// also returns the unwrapped repository.
func setupWith(t *testing.T, wrap func(port.SecretRequestRepository) port.SecretRequestRepository) (*secretreq.UseCase, *clock, port.SecretRequestRepository) {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.SeedServer(t, d, orgA, serverA)
	dbtest.SeedServer(t, d, orgB, serverB)
	dbtest.SeedMember(t, d, alice, orgA)
	dbtest.SeedMember(t, d, bob, orgB)
	repo, err := secretreqrepo.New(d.Tx, d.Driver)
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{now: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	var used port.SecretRequestRepository = repo
	if wrap != nil {
		used = wrap(repo)
	}
	return secretreq.New(secretreq.Deps{Repo: used, Orgs: orgs{alice: orgA, bob: orgB}, Servers: servers{},
		Apps: apps{}, Now: c.Now, RetryDelay: time.Millisecond}), c, repo
}

// flakyUse fails MarkUsed the first fails times (every time when fails < 0).
type flakyUse struct {
	port.SecretRequestRepository
	mu    sync.Mutex
	fails int
	tries int
}

func (f *flakyUse) MarkUsed(ctx context.Context, id, claimID string, now time.Time) (bool, error) {
	f.mu.Lock()
	f.tries++
	fail := f.fails < 0 || f.tries <= f.fails
	f.mu.Unlock()
	if fail {
		return false, errors.New("database is locked")
	}
	return f.SecretRequestRepository.MarkUsed(ctx, id, claimID, now)
}

// request creates alice's request for names on her server (app-1 when app).
func request(t *testing.T, u *secretreq.UseCase, app bool, names ...string) (secretreq.Request, string) {
	t.Helper()
	var appID *string
	if app {
		id := "app-1"
		appID = &id
	}
	req, raw, err := u.Create(context.Background(), alice, serverA, appID, names, map[string]string{names[0]: "a note"}, "for paperless")
	if err != nil {
		t.Fatal(err)
	}
	return req, raw
}

func TestCreateValidatesNames(t *testing.T) {
	u, _ := setup(t)
	ctx := context.Background()
	app := "app-1"
	newApp := "app-new"
	many := make([]string, secretreq.MaxNames+1)
	for i := range many {
		many[i] = "N" + strings.Repeat("A", i+1)
	}
	for _, c := range []struct {
		what  string
		names []string
		notes map[string]string
		msg   string
		appID *string
		want  string
	}{
		{"no names", nil, nil, "", nil, "names is required"},
		{"lower case", []string{"api_key"}, nil, "", nil, "invalid name"},
		{"leading digit", []string{"1KEY"}, nil, "", nil, "invalid name"},
		{"WF_ built-in", []string{"WF_TOKEN"}, nil, "", nil, "reserved"},
		{"compose project", []string{"COMPOSE_PROJECT_NAME"}, nil, "", nil, "reserved"},
		{"twice", []string{"A", "A"}, nil, "", nil, "twice"},
		{"too many", many, nil, "", nil, "at most 32"},
		{"note for another name", []string{"A"}, map[string]string{"B": "x"}, "", nil, "not in names"},
		{"long note", []string{"A"}, map[string]string{"A": strings.Repeat("x", 501)}, "", nil, "at most 500"},
		{"long message", []string{"A"}, nil, strings.Repeat("x", 2001), nil, "at most 2000"},
		{"invalid app id", []string{"A"}, nil, "", ptr("../x"), "invalid app_id"},
	} {
		_, _, err := u.Create(ctx, alice, serverA, c.appID, c.names, c.notes, c.msg)
		if !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want a rejection with %q", c.what, err, c.want)
		}
	}
	if _, _, err := u.Create(ctx, alice, serverB, nil, []string{"A"}, nil, ""); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("another org's server = %v", err)
	}
	// The limits themselves pass; a new app takes any name.
	req, raw, err := u.Create(ctx, alice, serverA, &newApp, []string{"ANY_NAME"},
		map[string]string{"ANY_NAME": strings.Repeat("x", 500)}, strings.Repeat("x", 2000))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "wfx_") || len(raw) < 4+43 || req.Status != model.SecretPending || req.AppID != newApp {
		t.Fatalf("created %+v, token %q", req, raw)
	}
	// An existing app's request may name a secret it does not have yet:
	// the save that uses the request is the binding check.
	if _, _, err := u.Create(ctx, alice, serverA, &app, []string{"API_KEY", "NEW_SECRET"}, nil, ""); err != nil {
		t.Fatalf("a new secret of an existing app: %v", err)
	}
}

func ptr(s string) *string { return &s }

func TestSecretRequestOtherOrgNotFound(t *testing.T) {
	u, _ := setup(t)
	ctx := context.Background()
	req, raw := request(t, u, true, "API_KEY")
	v, err := u.View(ctx, alice, raw)
	if err != nil {
		t.Fatal(err)
	}
	if v.ServerName != "homelab" || v.AppName != "Paperless" || v.Notes["API_KEY"] != "a note" || v.Message != "for paperless" {
		t.Fatalf("view = %+v", v)
	}
	_, unknown := u.View(ctx, alice, "wfx_"+strings.Repeat("A", 43))
	_, other := u.View(ctx, bob, raw)
	if !errors.Is(other, model.ErrNotFound) || other.Error() != unknown.Error() {
		t.Fatalf("another org's view = %v, an unknown token's = %v: they must read the same", other, unknown)
	}
	ct := map[string]string{"API_KEY": cryptotest.Ciphertext(t, "s3cret")}
	if err := u.Submit(ctx, bob, raw, ct); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("another org's submit = %v", err)
	}
	if _, err := u.Get(ctx, bob, req.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("another org's get = %v", err)
	}
	if _, _, err := u.Consume(ctx, bob, serverB, "", req.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("another org's consume = %v", err)
	}
}

func TestSubmitExactNames(t *testing.T) {
	u, _ := setup(t)
	ctx := context.Background()
	req, raw := request(t, u, false, "API_KEY", "DB_PASSWORD")
	ct := func() string { return cryptotest.Ciphertext(t, "v") }
	for _, values := range []map[string]string{
		{"API_KEY": ct()},
		{"API_KEY": ct(), "DB_PASSWORD": ct(), "EXTRA": ct()},
		{"API_KEY": ct(), "OTHER": ct()},
	} {
		if err := u.Submit(ctx, alice, raw, values); !errors.Is(err, model.ErrRejected) {
			t.Errorf("submit %v = %v, want a rejection", values, err)
		}
	}
	if err := u.Submit(ctx, alice, raw, map[string]string{"API_KEY": ct(), "DB_PASSWORD": ct()}); err != nil {
		t.Fatal(err)
	}
	got, err := u.Get(ctx, alice, req.ID)
	if err != nil || got.Status != model.SecretSubmitted || got.Ciphertexts != nil {
		t.Fatalf("get = %+v, %v (ciphertexts must not be shown)", got, err)
	}
}

func TestSubmitRejectsPlaintext(t *testing.T) {
	u, _ := setup(t)
	_, raw := request(t, u, false, "API_KEY")
	err := u.Submit(context.Background(), alice, raw, map[string]string{"API_KEY": "hunter2"})
	if !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), "not an encrypted value") {
		t.Fatalf("plaintext = %v", err)
	}
}

func TestSubmitTwiceRefused(t *testing.T) {
	u, _ := setup(t)
	ctx := context.Background()
	_, raw := request(t, u, false, "API_KEY")
	values := map[string]string{"API_KEY": cryptotest.Ciphertext(t, "v")}
	if err := u.Submit(ctx, alice, raw, values); err != nil {
		t.Fatal(err)
	}
	if err := u.Submit(ctx, alice, raw, values); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("second submit = %v", err)
	}
	if _, err := u.View(ctx, alice, raw); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("view after submit = %v", err)
	}
}

func TestExpired(t *testing.T) {
	u, c := setup(t)
	ctx := context.Background()
	stale, staleRaw := request(t, u, false, "API_KEY")
	unused, unusedRaw := request(t, u, false, "API_KEY")
	c.add(secretreq.SubmitWindow / 2)
	if err := u.Submit(ctx, alice, unusedRaw, map[string]string{"API_KEY": cryptotest.Ciphertext(t, "v")}); err != nil {
		t.Fatal(err)
	}
	c.add(secretreq.SubmitWindow / 2)
	if _, err := u.View(ctx, alice, staleRaw); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("view of an expired link = %v", err)
	}
	if got, _ := u.Get(ctx, alice, stale.ID); got.Status != model.SecretExpired {
		t.Fatalf("stale = %s", got.Status)
	}
	// The ciphertexts wait a day from their submission.
	if got, _ := u.Get(ctx, alice, unused.ID); got.Status != model.SecretSubmitted {
		t.Fatalf("submitted = %s", got.Status)
	}
	c.add(secretreq.UseWindow / 2)
	if got, _ := u.Get(ctx, alice, unused.ID); got.Status != model.SecretExpired {
		t.Fatalf("unused = %s", got.Status)
	}
	if _, _, err := u.Consume(ctx, alice, serverA, "", unused.ID); !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("consume expired = %v", err)
	}
}

func TestConsume(t *testing.T) {
	u, _ := setup(t)
	ctx := context.Background()
	req, raw := request(t, u, true, "API_KEY")
	if _, _, err := u.Consume(ctx, alice, serverA, "app-1", req.ID); err == nil || !strings.Contains(err.Error(), "not submitted") {
		t.Fatalf("consume pending = %v", err)
	}
	ct := cryptotest.Ciphertext(t, "v")
	if err := u.Submit(ctx, alice, raw, map[string]string{"API_KEY": ct}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := u.Consume(ctx, alice, serverA, "app-2", req.ID); err == nil || !strings.Contains(err.Error(), "another app") {
		t.Fatalf("consume for another app = %v", err)
	}
	if _, _, err := u.Consume(ctx, alice, serverB, "app-1", req.ID); err == nil || !strings.Contains(err.Error(), "another server") {
		t.Fatalf("consume for another server = %v", err)
	}
	if _, _, err := u.Consume(ctx, alice, serverA, "app-1", "not-an-id"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("consume of a bad id = %v", err)
	}
	got, claim, err := u.Consume(ctx, alice, serverA, "app-1", req.ID)
	if err != nil || got["API_KEY"] != ct {
		t.Fatalf("consume = %v, %v", got, err)
	}
	// Held by one save, it is refused to another and reads submitted.
	if _, _, err := u.Consume(ctx, alice, serverA, "app-1", req.ID); err == nil || !strings.Contains(err.Error(), "already being used") {
		t.Fatalf("consume while claimed = %v", err)
	}
	if r, _ := u.Get(ctx, alice, req.ID); r.Status != model.SecretSubmitted {
		t.Fatalf("claimed reads %s", r.Status)
	}
	if err := claim.Commit(false); err != nil {
		t.Fatal(err)
	}
	if err := claim.Commit(true); err != nil {
		t.Fatalf("a second commit answers the first's result: %v", err)
	}
	if r, _ := u.Get(ctx, alice, req.ID); r.Status != model.SecretSubmitted {
		t.Fatalf("after a refused save = %s", r.Status)
	}
	_, claim, err = u.Consume(ctx, alice, serverA, "app-1", req.ID)
	if err != nil {
		t.Fatalf("consume after a release = %v", err)
	}
	if err := claim.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	if err := claim.Commit(true); err != nil {
		t.Fatal(err)
	}
	if r, _ := u.Get(ctx, alice, req.ID); r.Status != model.SecretUsed {
		t.Fatalf("after a dispatched save = %s", r.Status)
	}
	if _, _, err := u.Consume(ctx, alice, serverA, "app-1", req.ID); err == nil || !strings.Contains(err.Error(), "used already") {
		t.Fatalf("consume used = %v", err)
	}
}

// TestClaimLapses: a crashed save's claim lapses after ClaimTTL; another
// save takes the request, and the first can neither hold nor end it.
func TestClaimLapses(t *testing.T) {
	u, c := setup(t)
	ctx := context.Background()
	req, raw := request(t, u, false, "API_KEY")
	if err := u.Submit(ctx, alice, raw, map[string]string{"API_KEY": cryptotest.Ciphertext(t, "v")}); err != nil {
		t.Fatal(err)
	}
	_, first, err := u.Consume(ctx, alice, serverA, "", req.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.add(secretreq.ClaimTTL)
	_, second, err := u.Consume(ctx, alice, serverA, "", req.ID)
	if err != nil {
		t.Fatalf("consume after the claim lapsed = %v", err)
	}
	if err := first.Dispatch(ctx); err == nil || !strings.Contains(err.Error(), "already being used") {
		t.Fatalf("the lapsed claim's dispatch = %v", err)
	}
	if err := first.Commit(true); err == nil || !strings.Contains(err.Error(), "claim was lost") {
		t.Fatalf("the lapsed claim's commit = %v", err)
	}
	if err := second.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	if err := second.Commit(true); err != nil {
		t.Fatal(err)
	}
	if r, _ := u.Get(ctx, alice, req.ID); r.Status != model.SecretUsed {
		t.Fatalf("status = %s", r.Status)
	}
}

// TestConcurrentConsume: of many saves racing for one request, one claims it.
func TestConcurrentConsume(t *testing.T) {
	u, _ := setup(t)
	ctx := context.Background()
	req, raw := request(t, u, false, "API_KEY")
	if err := u.Submit(ctx, alice, raw, map[string]string{"API_KEY": cryptotest.Ciphertext(t, "v")}); err != nil {
		t.Fatal(err)
	}
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		claimed int
	)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := u.Consume(ctx, alice, serverA, "", req.ID); err == nil {
				mu.Lock()
				claimed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if claimed != 1 {
		t.Fatalf("%d saves claimed the request, want 1", claimed)
	}
}

func TestSweep(t *testing.T) {
	u, c := setup(t)
	ctx := context.Background()
	old, _ := request(t, u, false, "API_KEY")
	c.add(secretreq.SubmitWindow)
	fresh, _ := request(t, u, false, "API_KEY")
	if err := u.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := u.Get(ctx, alice, old.ID); err != nil || got.Status != model.SecretExpired {
		t.Fatalf("old after sweep = %+v, %v", got, err)
	}
	if got, _ := u.Get(ctx, alice, fresh.ID); got.Status != model.SecretPending {
		t.Fatalf("fresh = %s", got.Status)
	}
	c.add(secretreq.Retention + time.Second)
	if err := u.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := u.Get(ctx, alice, old.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("old a week after it ended = %v, want pruned", err)
	}
	// fresh expired in the second sweep: kept for a week.
	if _, err := u.Get(ctx, alice, fresh.ID); err != nil {
		t.Fatalf("fresh = %v", err)
	}
}

// TestMarkUsedRetried: a transient error on MarkUsed is retried.
func TestMarkUsedRetried(t *testing.T) {
	flaky := &flakyUse{fails: 2}
	u, _, _ := setupWith(t, func(r port.SecretRequestRepository) port.SecretRequestRepository {
		flaky.SecretRequestRepository = r
		return flaky
	})
	ctx := context.Background()
	req, raw := request(t, u, false, "API_KEY")
	if err := u.Submit(ctx, alice, raw, map[string]string{"API_KEY": cryptotest.Ciphertext(t, "v")}); err != nil {
		t.Fatal(err)
	}
	_, claim, err := u.Consume(ctx, alice, serverA, "", req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := claim.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	if err := claim.Commit(true); err != nil || flaky.tries != 3 {
		t.Fatalf("commit = %v after %d tries, want success on the third", err, flaky.tries)
	}
	if r, _ := u.Get(ctx, alice, req.ID); r.Status != model.SecretUsed {
		t.Fatalf("status = %s", r.Status)
	}
}

// TestDispatchedNeverClaimedAgain: once a save marked the request
// dispatched, a MarkUsed that keeps failing leaves it unclaimable, past
// the claim's deadline too, and the sweep ends it used, deleting the
// ciphertexts.
func TestDispatchedNeverClaimedAgain(t *testing.T) {
	flaky := &flakyUse{fails: -1}
	u, c, repo := setupWith(t, func(r port.SecretRequestRepository) port.SecretRequestRepository {
		flaky.SecretRequestRepository = r
		return flaky
	})
	ctx := context.Background()
	req, raw := request(t, u, false, "API_KEY")
	if err := u.Submit(ctx, alice, raw, map[string]string{"API_KEY": cryptotest.Ciphertext(t, "v")}); err != nil {
		t.Fatal(err)
	}
	_, claim, err := u.Consume(ctx, alice, serverA, "", req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := claim.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	if err := claim.Commit(true); err == nil || flaky.tries != 4 {
		t.Fatalf("commit = %v after %d tries, want an error after 4", err, flaky.tries)
	}
	// The save's deferred release is a no-op after the commit.
	if err := claim.Commit(false); err == nil {
		t.Fatal("a release after a failed commit answers the commit's result")
	}
	c.add(secretreq.ClaimTTL + time.Second)
	if _, _, err := u.Consume(ctx, alice, serverA, "", req.ID); err == nil || !strings.Contains(err.Error(), "used already") {
		t.Fatalf("consume after the claim's deadline = %v", err)
	}
	if r, _ := u.Get(ctx, alice, req.ID); r.Status != model.SecretUsed {
		t.Fatalf("status = %s", r.Status)
	}
	if err := u.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, orgA, req.ID)
	if err != nil || got.Status != model.SecretUsed || got.Ciphertexts != nil {
		t.Fatalf("after the sweep = %+v, %v", got, err)
	}
}

// TestDispatchFailedKeepsUsed: a save whose send answered an error after
// Dispatch (a bus that may have delivered it anyway) does not release the
// request: its deferred Commit(false) marks it used, and no save takes it
// again.
func TestDispatchFailedKeepsUsed(t *testing.T) {
	u, c, repo := setupWith(t, nil)
	ctx := context.Background()
	req, raw := request(t, u, false, "API_KEY")
	if err := u.Submit(ctx, alice, raw, map[string]string{"API_KEY": cryptotest.Ciphertext(t, "v")}); err != nil {
		t.Fatal(err)
	}
	_, claim, err := u.Consume(ctx, alice, serverA, "", req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := claim.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	if err := claim.Commit(false); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, orgA, req.ID)
	if err != nil || got.Status != model.SecretUsed || got.Ciphertexts != nil {
		t.Fatalf("after a failed send = %+v, %v", got, err)
	}
	c.add(secretreq.ClaimTTL + time.Second)
	if _, _, err := u.Consume(ctx, alice, serverA, "", req.ID); err == nil || !strings.Contains(err.Error(), "used already") {
		t.Fatalf("consume after a failed send = %v", err)
	}
}

// TestRefusedBeforeDispatchReleases: a save refused before Dispatch
// returns the request to submitted for a retry.
func TestRefusedBeforeDispatchReleases(t *testing.T) {
	u, _ := setup(t)
	ctx := context.Background()
	req, raw := request(t, u, false, "API_KEY")
	if err := u.Submit(ctx, alice, raw, map[string]string{"API_KEY": cryptotest.Ciphertext(t, "v")}); err != nil {
		t.Fatal(err)
	}
	_, claim, err := u.Consume(ctx, alice, serverA, "", req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := claim.Commit(false); err != nil {
		t.Fatal(err)
	}
	if r, _ := u.Get(ctx, alice, req.ID); r.Status != model.SecretSubmitted {
		t.Fatalf("after a refused save = %s", r.Status)
	}
}
