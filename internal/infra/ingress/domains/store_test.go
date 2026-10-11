package domains

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/ingress/domains/certtest"
)

// pairFiles are the content-addressed pair names Put uses for cert/key.
func pairFiles(stem string, cert, key []byte) (string, string) {
	fp := fingerprint(cert, key)
	return stem + "." + fp + ".crt", stem + "." + fp + ".key"
}

func TestStorePutListDelete(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "domains")
	s := NewStore(dir)
	if s.Exists() {
		t.Fatal("fresh store must not exist yet")
	}
	if regs, err := s.List(); err != nil || regs != nil {
		t.Fatalf("missing dir: %v, %v", regs, err)
	}

	notAfter := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	cert, key := certtest.Issue(t, notAfter, "*.twillingate.app")
	put, err := s.Put("*.twillingate.app", model.DomainTLSCertificate, cert, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put("twillingate.app", model.DomainTLSAuto, nil, nil); err != nil {
		t.Fatal(err)
	}

	// Wildcards are spelled _wildcard_ on disk; the pair is named by its
	// content; the key is private.
	crt, keyFile := pairFiles("_wildcard_.twillingate.app", cert, key)
	for file, perm := range map[string]os.FileMode{
		"_wildcard_.twillingate.app.json": 0o644,
		crt:                               0o644,
		keyFile:                           0o600,
		"twillingate.app.json":            0o644,
	} {
		fi, err := os.Stat(filepath.Join(dir, file))
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if fi.Mode().Perm() != perm {
			t.Errorf("%s mode = %v, want %v", file, fi.Mode().Perm(), perm)
		}
	}

	regs, err := s.List()
	if err != nil || len(regs) != 2 {
		t.Fatalf("List = %+v, %v", regs, err)
	}
	if regs[0].Name != "*.twillingate.app" || regs[0].TLS != model.DomainTLSCertificate ||
		regs[0].CertFile != filepath.Join(dir, crt) || regs[0].KeyFile != filepath.Join(dir, keyFile) ||
		regs[0].NotAfter == nil || !regs[0].NotAfter.Equal(notAfter) || regs[0].Issuer != certtest.Issuer {
		t.Errorf("certificate registration = %+v", regs[0])
	}
	if regs[0].CertFile != put.CertFile || regs[0].Fingerprint != put.Fingerprint {
		t.Errorf("Put returned %+v, List %+v", put, regs[0])
	}
	if regs[1].Name != "twillingate.app" || regs[1].TLS != model.DomainTLSAuto || regs[1].CertFile != "" || regs[1].NotAfter != nil {
		t.Errorf("auto registration = %+v", regs[1])
	}

	// Replacing with another mode drops the certificate files.
	if _, err := s.Put("*.twillingate.app", model.DomainTLSOff, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := dirEntries(t, dir); len(got) != 2 {
		t.Errorf("pair survived the switch to off: %v", got)
	}

	existed, err := s.Delete("*.twillingate.app")
	if err != nil || !existed {
		t.Fatalf("Delete = %v, %v", existed, err)
	}
	if existed, _ := s.Delete("*.twillingate.app"); existed {
		t.Error("second delete reported the registration as existing")
	}
	if regs, _ := s.List(); len(regs) != 1 {
		t.Fatalf("after delete: %+v", regs)
	}
}

func TestStoreListSkipsBrokenFiles(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if _, err := s.Put("ok.app", model.DomainTLSOff, nil, nil); err != nil {
		t.Fatal(err)
	}
	for file, body := range map[string]string{
		"garbage.app.json": "{",
		"renamed.app.json": `{"name":"other.app","tls":"off"}`,
		"mode.app.json":    `{"name":"mode.app","tls":"manual"}`,
		"BAD.app.json":     `{"name":"BAD.app","tls":"off"}`,
		"nofp.app.json":    `{"name":"nofp.app","tls":"certificate"}`,
		"nopair.app.json":  `{"name":"nopair.app","tls":"certificate","fingerprint":"0123456789ab"}`,
		"notes.txt":        "x",
	} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	regs, err := s.List()
	if err != nil || len(regs) != 1 || regs[0].Name != "ok.app" {
		t.Fatalf("List = %+v, %v", regs, err)
	}
}

func TestStoreSeedRunsOnce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "domains")
	s := NewStore(dir)
	seeded, err := s.Seed([]model.RegisteredDomain{{Name: "a.app", TLS: model.DomainTLSAuto}, {Name: "b.app", TLS: model.DomainTLSOff}})
	if err != nil || !seeded {
		t.Fatalf("first seed = %v, %v", seeded, err)
	}
	if regs, _ := s.List(); len(regs) != 2 {
		t.Fatalf("seeded = %+v", regs)
	}
	if _, err := os.Stat(dir + ".seed"); !os.IsNotExist(err) {
		t.Error("staging dir left behind")
	}
	seeded, err = s.Seed([]model.RegisteredDomain{{Name: "c.app", TLS: model.DomainTLSAuto}})
	if err != nil || seeded {
		t.Fatalf("second seed = %v, %v", seeded, err)
	}
	if regs, _ := s.List(); len(regs) != 2 {
		t.Fatalf("second seed changed the store: %+v", regs)
	}
	// An empty install still gets the directory, so it never seeds again.
	empty := NewStore(filepath.Join(t.TempDir(), "domains"))
	if seeded, err := empty.Seed(nil); err != nil || !seeded || !empty.Exists() {
		t.Fatalf("empty seed = %v, %v", seeded, err)
	}
}

func TestCheckCertificate(t *testing.T) {
	now := time.Now()
	future := now.Add(90 * 24 * time.Hour)
	wildCert, wildKey := certtest.Issue(t, future, "*.twillingate.app", "twillingate.app")
	exactCert, exactKey := certtest.Issue(t, future, "app.twillingate.app")
	_, otherKey := certtest.Issue(t, future, "*.twillingate.app")
	oldCert, oldKey := certtest.Issue(t, now.Add(-time.Hour), "*.twillingate.app")
	interCert, _ := certtest.Issue(t, future, "intermediate.example")
	crlf := func(b []byte) []byte { return []byte(strings.ReplaceAll(string(b), "\n", "\r\n")) }
	concat := func(a, b []byte) []byte { return append(append([]byte{}, a...), b...) }

	type pair struct {
		label, name string
		cert, key   []byte
	}
	ok := []pair{
		{"wildcard cert, wildcard name", "*.twillingate.app", wildCert, wildKey},
		{"exact name under a wildcard cert", "app.twillingate.app", wildCert, wildKey},
		{"exact SAN", "twillingate.app", wildCert, wildKey},
		{"exact cert", "app.twillingate.app", exactCert, exactKey},
		// Review focus: PEM copied on Windows, and a chain with the leaf
		// first followed by an intermediate.
		{"CRLF line endings", "*.twillingate.app", crlf(wildCert), crlf(wildKey)},
		{"leaf then intermediate", "*.twillingate.app", concat(wildCert, interCert), wildKey},
	}
	for _, tc := range ok {
		leaf, err := CheckCertificate(tc.name, tc.cert, tc.key, now)
		if err != nil {
			t.Errorf("%s: %v", tc.label, err)
			continue
		}
		if leaf.Issuer.CommonName != certtest.Issuer {
			t.Errorf("%s: issuer = %q", tc.label, leaf.Issuer.CommonName)
		}
	}

	bad := []struct {
		pair
		want string
	}{
		{pair{"mismatched key", "*.twillingate.app", wildCert, otherKey}, "do not match"},
		{pair{"not PEM", "*.twillingate.app", []byte("nope"), wildKey}, "do not match"},
		{pair{"SAN not covering", "shop.example.com", wildCert, wildKey}, "does not cover shop.example.com"},
		{pair{"wildcard name, exact cert", "*.twillingate.app", exactCert, exactKey}, "does not cover *.twillingate.app (it covers app.twillingate.app)"},
		{pair{"two labels deep", "a.b.twillingate.app", wildCert, wildKey}, "does not cover"},
		{pair{"expired", "*.twillingate.app", oldCert, oldKey}, "expired on"},
		// Review focus: fields pasted the wrong way round, and a chain
		// whose leaf is not first.
		{pair{"cert and key swapped", "*.twillingate.app", wildKey, wildCert}, "do not match or are not PEM"},
		{pair{"intermediate first", "*.twillingate.app", concat(interCert, wildCert), wildKey}, "do not match"},
	}
	for _, tc := range bad {
		_, err := CheckCertificate(tc.name, tc.cert, tc.key, now)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want it to contain %q", tc.label, err, tc.want)
		}
	}
}

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestStoreRejectsInvalidNamesWithoutTouchingDisk(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "domains")
	s := NewStore(dir)
	if _, err := s.Put("../x", model.DomainTLSOff, nil, nil); err == nil {
		t.Error("Put accepted ../x")
	}
	if _, err := s.Put("ok.app", "manual", nil, nil); err == nil {
		t.Error("Put accepted an unknown mode")
	}
	// Review focus: a refused certificate stores nothing.
	cert, key := certtest.Issue(t, time.Now().Add(24*time.Hour), "a.app")
	if _, err := s.Put("a.app", model.DomainTLSCertificate, key, cert); err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Errorf("Put accepted a swapped pair: %v", err)
	}
	if _, err := s.Delete("a/b"); err == nil {
		t.Error("Delete accepted a/b")
	}
	if seeded, err := s.Seed([]model.RegisteredDomain{{Name: "ok.app", TLS: model.DomainTLSOff}, {Name: "../evil", TLS: model.DomainTLSOff}}); err == nil || seeded {
		t.Errorf("Seed accepted a bad name: %v, %v", seeded, err)
	}
	if _, err := s.Seed([]model.RegisteredDomain{{Name: "ok.app", TLS: "manual"}}); err == nil {
		t.Error("Seed accepted an unknown mode")
	}
	if _, err := s.Seed([]model.RegisteredDomain{{Name: "ok.app", TLS: model.DomainTLSCertificate}}); err == nil {
		t.Error("Seed accepted certificate mode without a certificate")
	}
	if got := dirEntries(t, root); len(got) != 0 {
		t.Fatalf("rejected calls touched disk: %v", got)
	}
}

// TestStoreRenewalIsContentAddressed: a renewal writes the new pair under
// new names, switches the .json to it, then prunes the old pair — the
// running proxy's files are never overwritten in place, and the paths (so
// the Caddy config) change with the certificate.
func TestStoreRenewalIsContentAddressed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "domains")
	s := NewStore(dir)
	c1, k1 := certtest.Issue(t, time.Now().Add(24*time.Hour), "a.app")
	c2, k2 := certtest.Issue(t, time.Now().Add(48*time.Hour), "a.app")
	first, err := s.Put("a.app", model.DomainTLSCertificate, c1, k1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Put("a.app", model.DomainTLSCertificate, c2, k2)
	if err != nil {
		t.Fatal(err)
	}
	if first.CertFile == second.CertFile || first.KeyFile == second.KeyFile {
		t.Fatalf("renewal kept the paths: %s", second.CertFile)
	}
	if got, _ := os.ReadFile(second.CertFile); string(got) != string(c2) {
		t.Error("renewal did not write the new certificate")
	}
	crt2, key2 := pairFiles("a.app", c2, k2)
	if got := dirEntries(t, dir); len(got) != 3 || !slices.Contains(got, crt2) || !slices.Contains(got, key2) {
		t.Fatalf("after renewal: %v (old pair or temp files left?)", got)
	}
	if regs, _ := s.List(); len(regs) != 1 || regs[0].CertFile != second.CertFile || !regs[0].NotAfter.Equal(*second.NotAfter) {
		t.Fatalf("renewed pair not listed: %+v", regs)
	}
	// Re-uploading the same pair is a no-op on disk.
	if again, err := s.Put("a.app", model.DomainTLSCertificate, c2, k2); err != nil || again.CertFile != second.CertFile {
		t.Fatalf("same pair again = %+v, %v", again, err)
	}
	if got := dirEntries(t, dir); len(got) != 3 {
		t.Fatalf("same pair again: %v", got)
	}
}

// TestStorePrunesOnlyItsOwnPairs: pruning parses file names instead of
// globbing, so a name that is a prefix of another never takes its files.
func TestStorePrunesOnlyItsOwnPairs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "domains")
	s := NewStore(dir)
	future := time.Now().Add(24 * time.Hour)
	for _, name := range []string{"x.app", "x.app.io"} {
		cert, key := certtest.Issue(t, future, name)
		if _, err := s.Put(name, model.DomainTLSCertificate, cert, key); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Delete("x.app"); err != nil {
		t.Fatal(err)
	}
	regs, _ := s.List()
	if len(regs) != 1 || regs[0].Name != "x.app.io" {
		t.Fatalf("deleting x.app took x.app.io's pair: %+v (dir %v)", regs, dirEntries(t, dir))
	}
	if got := dirEntries(t, dir); len(got) != 3 {
		t.Fatalf("x.app's pair left behind: %v", got)
	}
}

func TestStorePutCleansUpWhenWriteFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "domains")
	s := NewStore(dir)
	cert, key := certtest.Issue(t, time.Now().Add(24*time.Hour), "a.app")
	crt, keyFile := pairFiles("a.app", cert, key)
	// A directory squatting on the key's temp path makes its write fail.
	if err := os.MkdirAll(filepath.Join(dir, keyFile+".tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put("a.app", model.DomainTLSCertificate, cert, key); err == nil {
		t.Fatal("expected a write error")
	}
	for _, f := range []string{crt, crt + ".tmp", keyFile, "a.app.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); !os.IsNotExist(err) {
			t.Errorf("%s left behind", f)
		}
	}
}
