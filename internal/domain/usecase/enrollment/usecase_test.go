package enrollment

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"reflect"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/usecase/hubs"
	certpkg "winterflow/pkg/cert"
	"winterflow/pkg/util"
)

type fakeRepo struct {
	byID    map[string]*model.Enrollment
	servers map[string]model.Server
	hubIDs  map[string]string
	now     func() time.Time
}

func newFakeRepo(now func() time.Time) *fakeRepo {
	return &fakeRepo{byID: map[string]*model.Enrollment{}, servers: map[string]model.Server{}, hubIDs: map[string]string{}, now: now}
}

func (f *fakeRepo) Create(_ context.Context, e model.Enrollment) error {
	for _, o := range f.byID {
		if e.Code != "" && o.Code == e.Code {
			return errors.New("duplicate code")
		}
	}
	f.byID[e.ID] = &e
	return nil
}
func (f *fakeRepo) GetBySecretHash(_ context.Context, h string) (model.Enrollment, error) {
	for _, e := range f.byID {
		if e.PollSecretHash == h {
			return *e, nil
		}
	}
	return model.Enrollment{}, model.ErrNotFound
}
func (f *fakeRepo) GetByCode(_ context.Context, c string) (model.Enrollment, error) {
	for _, e := range f.byID {
		if e.Code == c && c != "" {
			return *e, nil
		}
	}
	return model.Enrollment{}, model.ErrNotFound
}
func (f *fakeRepo) ReplaceCode(_ context.Context, id, code string, exp time.Time, n int) error {
	e := f.byID[id]
	if e == nil || e.State != model.EnrollmentPending {
		return model.ErrNotFound
	}
	e.Code, e.CodeExpiresAt, e.CodesIssued = code, exp, n
	return nil
}
func (f *fakeRepo) MarkFailed(_ context.Context, id string) error {
	f.byID[id].State = model.EnrollmentFailed
	f.byID[id].Code = ""
	return nil
}
func (f *fakeRepo) Claim(_ context.Context, c port.ClaimEnrollment) (model.Server, error) {
	e := f.byID[c.EnrollmentID]
	if e == nil || e.State != model.EnrollmentPending || e.Code != c.Code || !c.Now.Before(e.CodeExpiresAt) {
		return model.Server{}, model.ErrRegistrationCodeExpired
	}
	e.State, e.ServerID, e.Certificate, e.HubAddr, e.ClaimedAt, e.Code = model.EnrollmentClaimed, c.ServerID, c.Certificate, c.HubAddr, c.Now, ""
	s := model.Server{ID: c.ServerID, OrganizationID: c.OrganizationID}
	f.servers[s.ID] = s
	f.hubIDs[s.ID] = c.HubID
	return s, nil
}
func (f *fakeRepo) CountPending(context.Context) (int, error) {
	n := 0
	for _, e := range f.byID {
		if e.State == model.EnrollmentPending {
			n++
		}
	}
	return n, nil
}
func (f *fakeRepo) CountPendingByIP(_ context.Context, ip string) (int, error) {
	n := 0
	for _, e := range f.byID {
		if e.State == model.EnrollmentPending && e.ClientIP == ip {
			n++
		}
	}
	return n, nil
}
func (f *fakeRepo) Prune(_ context.Context, now time.Time) error {
	for id, e := range f.byID {
		if e.State == model.EnrollmentClaimed && now.Sub(e.ClaimedAt) > 2*time.Hour {
			delete(f.byID, id)
		}
	}
	return nil
}

type fakePicker struct {
	hub model.Hub
	err error
}

func (p fakePicker) Pick(context.Context) (model.Hub, error) { return p.hub, p.err }

type caSigner struct {
	cert  *x509.Certificate
	key   *ecdsa.PrivateKey
	caPEM string
}

func newSigner(t *testing.T) *caSigner {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-ca"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour * 24), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return &caSigner{cert: cert, key: key, caPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))}
}

func (s *caSigner) Sign(csr, cn string) (string, time.Time, error) {
	return certpkg.SignCSRAs(csr, cn, s.cert, s.key, 1)
}
func (s *caSigner) CACertPEM() string { return s.caPEM }

func csrPEM(t *testing.T, curve elliptic.Curve) string {
	t.Helper()
	key, _ := ecdsa.GenerateKey(curve, rand.Reader)
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "ignored"}}, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

type env struct {
	uc   *UseCase
	repo *fakeRepo
	now  time.Time
	pick *fakePicker
	pins *fakePins
}

type fakePins struct {
	fps []string
	err error
}

func (p *fakePins) Active(context.Context) ([]string, error) { return p.fps, p.err }

func setup(t *testing.T) (*env, *time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	repo := newFakeRepo(clock)
	pick := &fakePicker{hub: model.Hub{ID: "hub-01.winterflow.dev", PublicAddr: "hub-01.winterflow.dev:443"}}
	pins := &fakePins{fps: []string{"aa", "bb"}}
	uc := New(repo, newSigner(t), picker{pick}, pins, clock)
	return &env{uc: uc, repo: repo, pick: pick, pins: pins}, &now
}

type picker struct{ p *fakePicker }

func (p picker) Pick(ctx context.Context) (model.Hub, error) { return p.p.Pick(ctx) }

var _ HubPicker = (*hubs.Picker)(nil)

func TestStartReturnsCodeAndSecret(t *testing.T) {
	e, now := setup(t)
	r, err := e.uc.Start(context.Background(), csrPEM(t, elliptic.P256()), "box", "203.0.113.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Code) != 6 || r.PollSecret == "" || !r.ExpiresAt.Equal(now.Add(10*time.Minute)) || r.CodesLeft != 9 {
		t.Fatalf("unexpected %+v", r)
	}
	for _, en := range e.repo.byID {
		if en.PollSecretHash == r.PollSecret || en.PollSecretHash != hashSecret(r.PollSecret) {
			t.Fatal("secret must be stored hashed")
		}
	}
}

func TestStartRejectsBadCSR(t *testing.T) {
	e, _ := setup(t)
	for _, c := range []string{"garbage", csrPEM(t, elliptic.P384())} {
		if _, err := e.uc.Start(context.Background(), c, "box", "203.0.113.1"); !errors.Is(err, ErrInvalidCSR) {
			t.Fatalf("want ErrInvalidCSR, got %v", err)
		}
	}
	if len(e.repo.byID) != 0 {
		t.Fatal("nothing must be stored")
	}
}

func TestStartTooManyPending(t *testing.T) {
	e, _ := setup(t)
	for i := 0; i < MaxPending; i++ {
		e.repo.byID[util.GenerateID()] = &model.Enrollment{State: model.EnrollmentPending}
	}
	if _, err := e.uc.Start(context.Background(), csrPEM(t, elliptic.P256()), "box", "203.0.113.1"); !errors.Is(err, ErrTooManyPending) {
		t.Fatalf("got %v", err)
	}
}

// One address cannot fill the pending table: past MaxPendingPerIP its
// starts are refused, while other addresses still enroll.
func TestStartTooManyPendingFromOneIP(t *testing.T) {
	e, _ := setup(t)
	ctx := context.Background()
	for i := 0; i < MaxPendingPerIP; i++ {
		if _, err := e.uc.Start(ctx, csrPEM(t, elliptic.P256()), "box", "198.51.100.4"); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
	}
	if _, err := e.uc.Start(ctx, csrPEM(t, elliptic.P256()), "box", "198.51.100.4"); !errors.Is(err, ErrTooManyPendingFromIP) {
		t.Fatalf("got %v", err)
	}
	if _, err := e.uc.Start(ctx, csrPEM(t, elliptic.P256()), "box", "198.51.100.5"); err != nil {
		t.Fatalf("other address refused: %v", err)
	}
	for _, en := range e.repo.byID {
		if en.ClientIP == "" {
			t.Fatalf("client ip not stored: %+v", en)
		}
	}
}

func TestRefreshIssuesNewCode(t *testing.T) {
	e, _ := setup(t)
	ctx := context.Background()
	r, _ := e.uc.Start(ctx, csrPEM(t, elliptic.P256()), "box", "203.0.113.1")
	r2, err := e.uc.Refresh(ctx, r.PollSecret)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Code == r.Code || r2.CodesLeft != 8 {
		t.Fatalf("got %+v", r2)
	}
	if _, err := e.repo.GetByCode(ctx, r.Code); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("old code must be gone")
	}
}

func TestRefreshCapAt10(t *testing.T) {
	e, _ := setup(t)
	ctx := context.Background()
	r, _ := e.uc.Start(ctx, csrPEM(t, elliptic.P256()), "box", "203.0.113.1")
	for i := 0; i < MaxCodes-1; i++ {
		if _, err := e.uc.Refresh(ctx, r.PollSecret); err != nil {
			t.Fatalf("refresh %d: %v", i, err)
		}
	}
	if _, err := e.uc.Refresh(ctx, r.PollSecret); !errors.Is(err, ErrNoCodesLeft) {
		t.Fatalf("got %v", err)
	}
	st, err := e.uc.Status(ctx, r.PollSecret)
	if err != nil || st.State != "failed" {
		t.Fatalf("status %+v %v", st, err)
	}
}

func TestStatusExpiredWhenCodeLapsed(t *testing.T) {
	e, now := setup(t)
	ctx := context.Background()
	r, _ := e.uc.Start(ctx, csrPEM(t, elliptic.P256()), "box", "203.0.113.1")
	st, _ := e.uc.Status(ctx, r.PollSecret)
	if st.State != "pending" || st.Code != r.Code {
		t.Fatalf("%+v", st)
	}
	*now = now.Add(10*time.Minute + time.Second)
	st, _ = e.uc.Status(ctx, r.PollSecret)
	if st.State != "expired" {
		t.Fatalf("%+v", st)
	}
	if _, err := e.uc.Claim(ctx, r.Code, "org"); !errors.Is(err, model.ErrRegistrationCodeExpired) {
		t.Fatalf("claim lapsed: %v", err)
	}
}

func TestClaimSignsAsServerID(t *testing.T) {
	e, _ := setup(t)
	ctx := context.Background()
	r, _ := e.uc.Start(ctx, csrPEM(t, elliptic.P256()), "box", "203.0.113.1")
	srv, err := e.uc.Claim(ctx, r.Code, "org-1")
	if err != nil {
		t.Fatal(err)
	}
	if e.repo.hubIDs[srv.ID] != "hub-01.winterflow.dev" {
		t.Fatalf("hub id %q", e.repo.hubIDs[srv.ID])
	}
	st, err := e.uc.Status(ctx, r.PollSecret)
	if err != nil || st.State != "claimed" {
		t.Fatalf("%+v %v", st, err)
	}
	block, _ := pem.Decode([]byte(st.Certificate))
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil || c.Subject.CommonName != srv.ID || st.ServerID != srv.ID {
		t.Fatalf("cn %v server %s", c, srv.ID)
	}
	if st.HubAddr != "hub-01.winterflow.dev:443" || st.CACertificate == "" || len(st.HubFingerprints) != 2 || st.HubFingerprints[0] != "aa" {
		t.Fatalf("%+v", st)
	}
	// Another replica (fresh instance, a picker that now fails) reads the
	// same hub from the row.
	other := New(e.repo, e.uc.signer, fakePicker{err: hubs.ErrNoHub}, e.pins, e.uc.now)
	st2, err := other.Status(ctx, r.PollSecret)
	if err != nil || st2.HubAddr != st.HubAddr || st2.Certificate != st.Certificate {
		t.Fatalf("fresh instance: %+v %v", st2, err)
	}
}

func TestClaimOldCodeAfterRefresh(t *testing.T) {
	e, _ := setup(t)
	ctx := context.Background()
	r, _ := e.uc.Start(ctx, csrPEM(t, elliptic.P256()), "box", "203.0.113.1")
	e.uc.Refresh(ctx, r.PollSecret)
	if _, err := e.uc.Claim(ctx, r.Code, "org"); !errors.Is(err, model.ErrInvalidRegistrationCode) {
		t.Fatalf("got %v", err)
	}
	if len(e.repo.servers) != 0 {
		t.Fatal("no server expected")
	}
}

func TestClaimNoHub(t *testing.T) {
	e, _ := setup(t)
	ctx := context.Background()
	r, _ := e.uc.Start(ctx, csrPEM(t, elliptic.P256()), "box", "203.0.113.1")
	e.pick.err = hubs.ErrNoHub
	if _, err := e.uc.Claim(ctx, r.Code, "org"); !errors.Is(err, hubs.ErrNoHub) {
		t.Fatalf("got %v", err)
	}
	en, err := e.repo.GetByCode(ctx, r.Code)
	if err != nil || en.State != model.EnrollmentPending || len(e.repo.servers) != 0 {
		t.Fatalf("enrollment must stay pending: %+v %v", en, err)
	}
}

func TestStatusClaimedIsIdempotent(t *testing.T) {
	e, now := setup(t)
	ctx := context.Background()
	r, _ := e.uc.Start(ctx, csrPEM(t, elliptic.P256()), "box", "203.0.113.1")
	if _, err := e.uc.Claim(ctx, r.Code, "org"); err != nil {
		t.Fatal(err)
	}
	a, _ := e.uc.Status(ctx, r.PollSecret)
	b, _ := e.uc.Status(ctx, r.PollSecret)
	if a.Certificate == "" || !reflect.DeepEqual(a, b) {
		t.Fatal("claimed status must repeat")
	}
	// Past ClaimedTTL the certificate is no longer handed out, but the agent
	// learns why instead of reading "unknown enrollment".
	*now = now.Add(ClaimedTTL + time.Second)
	st, err := e.uc.Status(ctx, r.PollSecret)
	if err != nil || st.State != "uncollected" || st.Certificate != "" || st.ServerID != "" {
		t.Fatalf("past ClaimedTTL: %+v %v", st, err)
	}
	*now = now.Add(3 * time.Hour)
	e.repo.Prune(ctx, *now)
	if _, err := e.uc.Status(ctx, r.PollSecret); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("after prune: %v", err)
	}
}

func TestStatusClaimedNeedsPins(t *testing.T) {
	e, _ := setup(t)
	ctx := context.Background()
	r, _ := e.uc.Start(ctx, csrPEM(t, elliptic.P256()), "box", "203.0.113.1")
	// Pending answers do not read the set.
	e.pins.err = errors.New("db down")
	if st, err := e.uc.Status(ctx, r.PollSecret); err != nil || st.State != "pending" || st.HubFingerprints != nil {
		t.Fatalf("pending: %+v %v", st, err)
	}
	if _, err := e.uc.Claim(ctx, r.Code, "org-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.uc.Status(ctx, r.PollSecret); !errors.Is(err, ErrPinsUnavailable) {
		t.Fatalf("claimed without pins: err = %v", err)
	}
	// None trusted yet: an empty set, never nil.
	e.pins.err, e.pins.fps = nil, nil
	if st, err := e.uc.Status(ctx, r.PollSecret); err != nil || st.State != "claimed" || st.HubFingerprints == nil {
		t.Fatalf("empty: %+v %v", st, err)
	}
}
