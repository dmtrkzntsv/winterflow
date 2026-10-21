package enrollment

import (
	"errors"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
)

var (
	org  = dbtest.ID(1)
	srv  = dbtest.ID(11)
	cert = dbtest.ID(12)
)

func newRepo(t *testing.T) (*Repo, *db.DB) {
	t.Helper()
	d := dbtest.Open(t)
	r, err := New(d.Tx, d.Driver)
	if err != nil {
		t.Fatal(err)
	}
	return r, d
}

// now is whole-second UTC: the stored format has no sub-second part.
func now() time.Time { return time.Now().UTC().Truncate(time.Second) }

func enrollment(n int, code string, expires time.Time) model.Enrollment {
	return model.Enrollment{
		ID:             dbtest.ID(100 + n),
		PollSecretHash: strings.Repeat("a", 60) + string(rune('a'+n)) + "bc",
		CSR:            "csr",
		Hostname:       "box",
		Code:           code,
		CodeExpiresAt:  expires,
		CodesIssued:    1,
		State:          model.EnrollmentPending,
		CreatedAt:      now(),
	}
}

func claim(e model.Enrollment, at time.Time) port.ClaimEnrollment {
	return port.ClaimEnrollment{
		EnrollmentID: e.ID, Code: e.Code, ServerID: srv, OrganizationID: org,
		HubID: "hub-01.winterflow.dev", HubAddr: "hub-01.winterflow.dev:443", CertificateID: cert, Certificate: "PEM",
		CertExpiresAt: at.Add(24 * time.Hour), Now: at,
	}
}

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql"); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestEnrollmentCreateGet(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	e := enrollment(1, "ABC123", now().Add(10*time.Minute))
	if err := r.Create(ctx, e); err != nil {
		t.Fatal(err)
	}
	byHash, err := r.GetBySecretHash(ctx, e.PollSecretHash)
	if err != nil {
		t.Fatal(err)
	}
	if byHash != e {
		t.Fatalf("got %+v, want %+v", byHash, e)
	}
	byCode, err := r.GetByCode(ctx, "ABC123")
	if err != nil || byCode.ID != e.ID {
		t.Fatalf("GetByCode = %+v, %v", byCode, err)
	}
	if _, err := r.GetBySecretHash(ctx, "nope"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("missing hash: %v", err)
	}
	if _, err := r.GetByCode(ctx, "ZZZZZZ"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("missing code: %v", err)
	}
}

func TestReplaceCodeOnlyPending(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	e := enrollment(1, "ABC123", now().Add(time.Minute))
	if err := r.Create(ctx, e); err != nil {
		t.Fatal(err)
	}
	exp := now().Add(10 * time.Minute)
	if err := r.ReplaceCode(ctx, e.ID, "NEW456", exp, 2); err != nil {
		t.Fatal(err)
	}
	got, err := r.GetByCode(ctx, "NEW456")
	if err != nil || got.CodesIssued != 2 || !got.CodeExpiresAt.Equal(exp) {
		t.Fatalf("after replace: %+v, %v", got, err)
	}
	if _, err := r.GetByCode(ctx, "ABC123"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("old code still resolves: %v", err)
	}
	if err := r.MarkFailed(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.ReplaceCode(ctx, e.ID, "THIRD7", exp, 3); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("replace on failed: %v", err)
	}
	f, err := r.GetBySecretHash(ctx, e.PollSecretHash)
	if err != nil || f.State != model.EnrollmentFailed || f.Code != "" {
		t.Fatalf("failed row: %+v, %v", f, err)
	}
}

func TestClaimCreatesServerAndCert(t *testing.T) {
	r, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedOrg(t, d, org)
	e := enrollment(1, "ABC123", now().Add(10*time.Minute))
	e.Hostname = strings.Repeat("h", 100)
	if err := r.Create(ctx, e); err != nil {
		t.Fatal(err)
	}
	s, err := r.Claim(ctx, claim(e, now()))
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != srv || s.OrganizationID != org || s.Name != strings.Repeat("h", 64) {
		t.Fatalf("server = %+v", s)
	}
	if got := dbtest.QueryString(t, d, `SELECT organization_id FROM servers WHERE server_id = ?`, srv); got != org {
		t.Fatalf("server org = %q", got)
	}
	if got := dbtest.QueryString(t, d, `SELECT hub_id FROM servers WHERE server_id = ?`, srv); got != "hub-01.winterflow.dev" {
		t.Fatalf("hub_id = %q", got)
	}
	if got := dbtest.QueryString(t, d, `SELECT certificate FROM server_certificates WHERE server_id = ? AND is_active = ?`, srv, true); got != "PEM" {
		t.Fatalf("certificate = %q", got)
	}
	got, err := r.GetBySecretHash(ctx, e.PollSecretHash)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != model.EnrollmentClaimed || got.Code != "" || got.ServerID != srv || got.Certificate != "PEM" || got.HubAddr != "hub-01.winterflow.dev:443" || got.ClaimedAt.IsZero() {
		t.Fatalf("claimed row: %+v", got)
	}
}

func TestClaimExpiredCode(t *testing.T) {
	r, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedOrg(t, d, org)
	e := enrollment(1, "ABC123", now().Add(-time.Minute))
	if err := r.Create(ctx, e); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Claim(ctx, claim(e, now())); !errors.Is(err, model.ErrRegistrationCodeExpired) {
		t.Fatalf("err = %v", err)
	}
	if got := dbtest.QueryString(t, d, `SELECT COUNT(*) FROM servers`); got != "0" {
		t.Fatalf("servers = %s", got)
	}
	assertPending(t, r, e)
}

func TestClaimTwiceSecondFails(t *testing.T) {
	r, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedOrg(t, d, org)
	e := enrollment(1, "ABC123", now().Add(10*time.Minute))
	if err := r.Create(ctx, e); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Claim(ctx, claim(e, now())); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Claim(ctx, claim(e, now())); !errors.Is(err, model.ErrRegistrationCodeExpired) {
		t.Fatalf("second claim: %v", err)
	}
	if got := dbtest.QueryString(t, d, `SELECT COUNT(*) FROM servers`); got != "1" {
		t.Fatalf("servers = %s", got)
	}
	got, err := r.GetBySecretHash(ctx, e.PollSecretHash)
	if err != nil || got.State != model.EnrollmentClaimed || got.ServerID != srv {
		t.Fatalf("row changed by second claim: %+v, %v", got, err)
	}
}

func TestPrune(t *testing.T) {
	r, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	n := now()
	cases := []struct {
		state   model.EnrollmentState
		created time.Time
		claimed time.Time
		keep    bool
	}{
		{model.EnrollmentPending, n.Add(-119 * time.Minute), time.Time{}, true},
		{model.EnrollmentPending, n.Add(-121 * time.Minute), time.Time{}, false},
		{model.EnrollmentFailed, n.Add(-30 * time.Minute), time.Time{}, true},
		{model.EnrollmentFailed, n.Add(-3 * time.Hour), time.Time{}, false},
		{model.EnrollmentClaimed, n.Add(-5 * time.Hour), n.Add(-14 * time.Minute), true},
		{model.EnrollmentClaimed, n.Add(-5 * time.Hour), n.Add(-16 * time.Minute), false},
	}
	for i, c := range cases {
		e := enrollment(i, "", time.Time{})
		e.State, e.CreatedAt, e.ClaimedAt = c.state, c.created, c.claimed
		if err := r.Create(ctx, e); err != nil {
			t.Fatal(err)
		}
		if !c.claimed.IsZero() {
			dbtest.Exec(t, d, `UPDATE agent_enrollments SET claimed_at = ? WHERE enrollment_id = ?`, c.claimed, e.ID)
		}
	}
	if err := r.Prune(ctx, n); err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		_, err := r.GetBySecretHash(ctx, enrollment(i, "", time.Time{}).PollSecretHash)
		if kept := err == nil; kept != c.keep {
			t.Errorf("case %d (%s, created %s ago): kept=%v want %v (%v)", i, c.state, n.Sub(c.created), kept, c.keep, err)
		}
	}
}

func TestCountPending(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	for i, st := range []model.EnrollmentState{model.EnrollmentPending, model.EnrollmentPending, model.EnrollmentFailed, model.EnrollmentClaimed} {
		e := enrollment(i, "", time.Time{})
		e.State = st
		if err := r.Create(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.CountPending(ctx)
	if err != nil || got != 2 {
		t.Fatalf("CountPending = %d, %v", got, err)
	}
}

// assertPending checks the enrollment is exactly as created: pending, code
// intact and still resolvable.
func assertPending(t *testing.T, r *Repo, want model.Enrollment) {
	t.Helper()
	ctx := dbtest.Ctx(t)
	got, err := r.GetBySecretHash(ctx, want.PollSecretHash)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("enrollment changed: got %+v, want %+v", got, want)
	}
	if byCode, err := r.GetByCode(ctx, want.Code); err != nil || byCode.ID != want.ID {
		t.Fatalf("GetByCode = %+v, %v", byCode, err)
	}
}

func TestClaimIsAtomic(t *testing.T) {
	r, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedServer(t, d, org, srv) // the claim's server id is already taken
	e := enrollment(1, "ABC123", now().Add(10*time.Minute))
	if err := r.Create(ctx, e); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Claim(ctx, claim(e, now())); err == nil {
		t.Fatal("claim with a duplicate server id succeeded")
	}
	assertPending(t, r, e)
	if got := dbtest.QueryString(t, d, `SELECT COUNT(*) FROM servers`); got != "1" {
		t.Fatalf("servers = %s", got)
	}
	if got := dbtest.QueryString(t, d, `SELECT COUNT(*) FROM server_certificates`); got != "0" {
		t.Fatalf("certificates = %s", got)
	}
}

func TestClaimWrongCodeOrFailedState(t *testing.T) {
	r, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedOrg(t, d, org)
	e := enrollment(1, "ABC123", now().Add(10*time.Minute))
	if err := r.Create(ctx, e); err != nil {
		t.Fatal(err)
	}
	wrong := claim(e, now())
	wrong.Code = "WRONG1"
	if _, err := r.Claim(ctx, wrong); !errors.Is(err, model.ErrRegistrationCodeExpired) {
		t.Fatalf("wrong code: %v", err)
	}
	assertPending(t, r, e)

	if err := r.MarkFailed(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Claim(ctx, claim(e, now())); !errors.Is(err, model.ErrRegistrationCodeExpired) {
		t.Fatalf("failed enrollment: %v", err)
	}
	if got := dbtest.QueryString(t, d, `SELECT COUNT(*) FROM servers`); got != "0" {
		t.Fatalf("servers = %s", got)
	}
}
