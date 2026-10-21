package hubjoin_test

import (
	"context"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/usecase/enrollment/enrolltest"
	"winterflow/internal/domain/usecase/hubjoin"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/internal/infra/db/repository/hubtoken"
)

type fixture struct {
	uc   *hubjoin.UseCase
	d    *db.DB
	now  *atomic.Int64 // unix seconds
	live *liveHubs
}

// liveHubs is the hub list a test edits.
type liveHubs struct {
	mu   sync.Mutex
	hubs []model.Hub
	err  error
}

func (l *liveHubs) List(context.Context) ([]model.Hub, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]model.Hub(nil), l.hubs...), l.err
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	d := dbtest.Open(t)
	repo, err := hubtoken.New(d.Tx, d.Driver)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{d: d, now: &atomic.Int64{}, live: &liveHubs{}}
	f.now.Store(time.Now().Unix())
	clock := func() time.Time { return time.Unix(f.now.Load(), 0).UTC() }
	f.uc = hubjoin.New(repo, enrolltest.Signer(t), f.live, clock)
	return f
}

func TestIssueRedeem(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	token, exp, err := f.uc.Issue(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "wfh_") || len(token) != 4+43 {
		t.Fatalf("token = %q", token)
	}
	if want := time.Unix(f.now.Load(), 0).Add(hubjoin.DefaultTTL); !exp.Equal(want) {
		t.Fatalf("expires %v, want %v", exp, want)
	}
	sum := sha256.Sum256([]byte(token))
	if got := dbtest.QueryString(t, f.d, `SELECT COUNT(*) FROM hub_join_tokens WHERE token_hash = '`+hex.EncodeToString(sum[:])+`'`); got != "1" {
		t.Fatalf("hashed row count = %s", got)
	}
	if got := dbtest.QueryString(t, f.d, `SELECT COUNT(*) FROM hub_join_tokens WHERE token_hash = '`+token+`'`); got != "0" {
		t.Fatal("plaintext token stored")
	}

	certPEM, caPEM, err := f.uc.Redeem(ctx, token, enrolltest.CSR(t, elliptic.P256()), "hub-01.example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(certPEM))
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject.CommonName != "hub-01.example.com" || len(c.DNSNames) != 1 || c.DNSNames[0] != "hub-01.example.com" {
		t.Fatalf("cert CN %q SANs %v", c.Subject.CommonName, c.DNSNames)
	}
	if !strings.Contains(caPEM, "BEGIN CERTIFICATE") {
		t.Fatalf("ca = %q", caPEM)
	}
	if got := dbtest.QueryString(t, f.d, `SELECT used_by_hub FROM hub_join_tokens`); got != "hub-01.example.com" {
		t.Fatalf("used_by_hub = %q", got)
	}
}

func TestRedeemTwice(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	token, _, _ := f.uc.Issue(ctx, time.Hour)
	csr := enrolltest.CSR(t, elliptic.P256())
	if _, _, err := f.uc.Redeem(ctx, token, csr, "h.example.com:443"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.uc.Redeem(ctx, token, csr, "h.example.com:443"); !errors.Is(err, hubjoin.ErrInvalidToken) {
		t.Fatalf("second redeem = %v", err)
	}
}

func TestRedeemUnknown(t *testing.T) {
	f := newFixture(t)
	if _, _, err := f.uc.Redeem(context.Background(), "wfh_nope", enrolltest.CSR(t, elliptic.P256()), "h.example.com:443"); !errors.Is(err, hubjoin.ErrInvalidToken) {
		t.Fatalf("err = %v", err)
	}
}

func TestRedeemExpired(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	token, _, _ := f.uc.Issue(ctx, time.Minute)
	f.now.Add(61)
	if _, _, err := f.uc.Redeem(ctx, token, enrolltest.CSR(t, elliptic.P256()), "h.example.com:443"); !errors.Is(err, hubjoin.ErrInvalidToken) {
		t.Fatalf("err = %v", err)
	}
}

func TestRedeemConcurrent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	token, _, _ := f.uc.Issue(ctx, time.Hour)
	csr := enrolltest.CSR(t, elliptic.P256())
	var wins, losses atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := f.uc.Redeem(ctx, token, csr, "h.example.com:443")
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, hubjoin.ErrInvalidToken):
				losses.Add(1)
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || losses.Load() != 1 {
		t.Fatalf("wins %d losses %d", wins.Load(), losses.Load())
	}
}

func TestRedeemBadInputKeepsToken(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	token, _, _ := f.uc.Issue(ctx, time.Hour)
	good := enrolltest.CSR(t, elliptic.P256())
	for _, addr := range []string{"", "host", "h.example.com:0", "h.example.com:x", ":443", "bad host:443", "h_x.example.com:443", strings.Repeat("a", 64) + ".com:443"} {
		if _, _, err := f.uc.Redeem(ctx, token, good, addr); !errors.Is(err, hubjoin.ErrInvalidAddr) {
			t.Errorf("addr %q = %v", addr, err)
		}
	}
	for _, csr := range []string{"", "garbage", enrolltest.CSR(t, elliptic.P384())} {
		if _, _, err := f.uc.Redeem(ctx, token, csr, "h.example.com:443"); !errors.Is(err, hubjoin.ErrInvalidCSR) {
			t.Errorf("csr = %v", err)
		}
	}
	// None of that spent the token; an IP address is a valid host too.
	if _, _, err := f.uc.Redeem(ctx, token, good, "203.0.113.7:443"); err != nil {
		t.Fatalf("token was spent: %v", err)
	}
}

func TestIssuePrunes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.uc.Issue(ctx, time.Minute); err != nil {
		t.Fatal(err)
	}
	f.now.Add(int64(25 * time.Hour / time.Second))
	if _, _, err := f.uc.Issue(ctx, time.Minute); err != nil {
		t.Fatal(err)
	}
	if got := dbtest.QueryString(t, f.d, `SELECT COUNT(*) FROM hub_join_tokens`); got != "1" {
		t.Fatalf("%s rows, want only the fresh one", got)
	}
}

// A live hub's id cannot be taken; the check runs before the token is
// spent, and ids compare case-insensitively.
func TestRedeemRefusesLiveHubID(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	token, _, _ := f.uc.Issue(ctx, time.Hour)
	csr := enrolltest.CSR(t, elliptic.P256())
	f.live.hubs = []model.Hub{{ID: "hub-01.example.com", Accepting: true}}
	for _, addr := range []string{"hub-01.example.com:443", "HUB-01.Example.com:50051"} {
		if _, _, err := f.uc.Redeem(ctx, token, csr, addr); !errors.Is(err, hubjoin.ErrHubLive) || !errors.Is(err, hubjoin.ErrInvalidAddr) {
			t.Fatalf("%s: err = %v, want ErrHubLive (an ErrInvalidAddr)", addr, err)
		}
	}
	// A list that cannot be read refuses too, without spending the token.
	f.live.err = errors.New("redis down")
	if _, _, err := f.uc.Redeem(ctx, token, csr, "hub-02.example.com:443"); err == nil || errors.Is(err, hubjoin.ErrInvalidToken) {
		t.Fatalf("list error: err = %v", err)
	}
	f.live.err = nil
	if _, _, err := f.uc.Redeem(ctx, token, csr, "hub-02.example.com:443"); err != nil {
		t.Fatalf("token was spent: %v", err)
	}
}

// The hub id is the lower-cased host, in the token row and the certificate.
func TestRedeemLowercasesHubID(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	token, _, _ := f.uc.Issue(ctx, time.Hour)
	certPEM, _, err := f.uc.Redeem(ctx, token, enrolltest.CSR(t, elliptic.P256()), "Hub-07.Example.COM:443")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(certPEM))
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if cert.Subject.CommonName != "hub-07.example.com" || len(cert.DNSNames) != 1 || cert.DNSNames[0] != "hub-07.example.com" {
		t.Fatalf("cert CN %q SANs %v", cert.Subject.CommonName, cert.DNSNames)
	}
}
