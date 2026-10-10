package domains

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/ingress/domains/certtest"
)

func TestStorePutListDelete(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "domains")
	s := NewStore(dir)
	if s.Exists() {
		t.Fatal("fresh store must not exist yet")
	}
	if regs, err := s.List(); err != nil || regs != nil {
		t.Fatalf("missing dir: %v, %v", regs, err)
	}

	cert, key := certtest.Issue(t, time.Now().Add(24*time.Hour), "*.twillingate.app")
	if err := s.Put("*.twillingate.app", command.DomainTLSCertificate, cert, key); err != nil {
		t.Fatal(err)
	}
	if err := s.Put("twillingate.app", command.DomainTLSAuto, nil, nil); err != nil {
		t.Fatal(err)
	}

	// Wildcards are spelled _wildcard_ on disk; the key is private.
	for file, perm := range map[string]os.FileMode{
		"_wildcard_.twillingate.app.json": 0o644,
		"_wildcard_.twillingate.app.crt":  0o644,
		"_wildcard_.twillingate.app.key":  0o600,
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
	if regs[0].Name != "*.twillingate.app" || regs[0].TLS != command.DomainTLSCertificate ||
		regs[0].CertFile != filepath.Join(dir, "_wildcard_.twillingate.app.crt") ||
		regs[0].KeyFile != filepath.Join(dir, "_wildcard_.twillingate.app.key") {
		t.Errorf("certificate registration = %+v", regs[0])
	}
	if regs[1].Name != "twillingate.app" || regs[1].TLS != command.DomainTLSAuto || regs[1].CertFile != "" {
		t.Errorf("auto registration = %+v", regs[1])
	}

	// Replacing with another mode drops the certificate files.
	if err := s.Put("*.twillingate.app", command.DomainTLSOff, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "_wildcard_.twillingate.app.key")); !os.IsNotExist(err) {
		t.Error("key survived the switch to off")
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
	if err := s.Put("ok.app", command.DomainTLSOff, nil, nil); err != nil {
		t.Fatal(err)
	}
	for file, body := range map[string]string{
		"garbage.app.json": "{",
		"renamed.app.json": `{"name":"other.app","tls":"off"}`,
		"mode.app.json":    `{"name":"mode.app","tls":"manual"}`,
		"BAD.app.json":     `{"name":"BAD.app","tls":"off"}`,
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
	seeded, err := s.Seed([]Registration{{Name: "a.app", TLS: command.DomainTLSAuto}, {Name: "b.app", TLS: command.DomainTLSOff}})
	if err != nil || !seeded {
		t.Fatalf("first seed = %v, %v", seeded, err)
	}
	if regs, _ := s.List(); len(regs) != 2 {
		t.Fatalf("seeded = %+v", regs)
	}
	if _, err := os.Stat(dir + ".seed"); !os.IsNotExist(err) {
		t.Error("staging dir left behind")
	}
	seeded, err = s.Seed([]Registration{{Name: "c.app", TLS: command.DomainTLSAuto}})
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

func TestCertInfo(t *testing.T) {
	dir := t.TempDir()
	notAfter := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	cert, _ := certtest.Issue(t, notAfter, "x.app")
	path := filepath.Join(dir, "x.crt")
	if err := os.WriteFile(path, cert, 0o644); err != nil {
		t.Fatal(err)
	}
	got, issuer, err := CertInfo(path)
	if err != nil || !got.Equal(notAfter) || issuer != certtest.Issuer {
		t.Fatalf("CertInfo = %v, %q, %v", got, issuer, err)
	}
	if err := os.WriteFile(path, []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CertInfo(path); err == nil {
		t.Error("junk must not parse")
	}
	if _, _, err := CertInfo(filepath.Join(dir, "missing.crt")); err == nil {
		t.Error("missing file must error")
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
	if err := s.Put("../x", command.DomainTLSOff, nil, nil); err == nil {
		t.Error("Put accepted ../x")
	}
	if err := s.Put("ok.app", "manual", nil, nil); err == nil {
		t.Error("Put accepted an unknown mode")
	}
	if _, err := s.Delete("a/b"); err == nil {
		t.Error("Delete accepted a/b")
	}
	if seeded, err := s.Seed([]Registration{{Name: "ok.app", TLS: command.DomainTLSOff}, {Name: "../evil", TLS: command.DomainTLSOff}}); err == nil || seeded {
		t.Errorf("Seed accepted a bad name: %v, %v", seeded, err)
	}
	if _, err := s.Seed([]Registration{{Name: "ok.app", TLS: "manual"}}); err == nil {
		t.Error("Seed accepted an unknown mode")
	}
	if got := dirEntries(t, root); len(got) != 0 {
		t.Fatalf("rejected calls touched disk: %v", got)
	}
}

func TestStoreListSkipsMismatchedCertificatePair(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "domains")
	s := NewStore(dir)
	cert, key := certtest.Issue(t, time.Now().Add(24*time.Hour), "a.app")
	if err := s.Put("a.app", command.DomainTLSCertificate, cert, key); err != nil {
		t.Fatal(err)
	}
	if regs, _ := s.List(); len(regs) != 1 {
		t.Fatalf("good pair not listed: %+v", regs)
	}
	// A crash between the two renames of a renewal leaves cert and key from
	// different pairs.
	_, otherKey := certtest.Issue(t, time.Now().Add(24*time.Hour), "a.app")
	if err := os.WriteFile(filepath.Join(dir, "a.app.key"), otherKey, 0o600); err != nil {
		t.Fatal(err)
	}
	if regs, _ := s.List(); len(regs) != 0 {
		t.Fatalf("mismatched pair must be skipped: %+v", regs)
	}
}

func TestStorePutRenewalReplacesPairAndLeavesNoTemp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "domains")
	s := NewStore(dir)
	c1, k1 := certtest.Issue(t, time.Now().Add(24*time.Hour), "a.app")
	c2, k2 := certtest.Issue(t, time.Now().Add(48*time.Hour), "a.app")
	for _, p := range [][2][]byte{{c1, k1}, {c2, k2}} {
		if err := s.Put("a.app", command.DomainTLSCertificate, p[0], p[1]); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "a.app.crt")); string(got) != string(c2) {
		t.Error("renewal did not replace the certificate")
	}
	for _, n := range dirEntries(t, dir) {
		if strings.HasSuffix(n, ".tmp") {
			t.Errorf("temp file left behind: %s", n)
		}
	}
	if regs, _ := s.List(); len(regs) != 1 {
		t.Fatalf("renewed pair not listed: %+v", regs)
	}
}

func TestStorePutCleansTempWhenWriteFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "domains")
	s := NewStore(dir)
	cert, key := certtest.Issue(t, time.Now().Add(24*time.Hour), "a.app")
	// A directory squatting on the key's temp path makes the second stage fail.
	if err := os.MkdirAll(filepath.Join(dir, "a.app.key.tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Put("a.app", command.DomainTLSCertificate, cert, key); err == nil {
		t.Fatal("expected a write error")
	}
	if _, err := os.Stat(filepath.Join(dir, "a.app.crt.tmp")); !os.IsNotExist(err) {
		t.Error("certificate temp file left behind")
	}
	if _, err := os.Stat(filepath.Join(dir, "a.app.crt")); !os.IsNotExist(err) {
		t.Error("certificate was installed although the key could not be written")
	}
}

func TestCertInfoSkipsNonCertificateBlocks(t *testing.T) {
	notAfter := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	cert, key := certtest.Issue(t, notAfter, "x.app")
	path := filepath.Join(t.TempDir(), "x.pem")
	if err := os.WriteFile(path, append(append([]byte{}, key...), cert...), 0o644); err != nil {
		t.Fatal(err)
	}
	got, issuer, err := CertInfo(path)
	if err != nil || !got.Equal(notAfter) || issuer != certtest.Issuer {
		t.Fatalf("CertInfo = %v, %q, %v", got, issuer, err)
	}
	if err := os.WriteFile(path, key, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CertInfo(path); err == nil {
		t.Error("a file with no CERTIFICATE block must error")
	}
}
