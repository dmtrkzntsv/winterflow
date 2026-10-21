package cert

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEnsureHubPKI(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pki")
	changed, err := EnsureHubPKI(dir, []string{"hub-01.example.com", "10.0.0.5"})
	if err != nil || !changed {
		t.Fatalf("first call: changed=%v err=%v", changed, err)
	}
	for _, f := range []string{"ca.crt", "ca.key", "hub.crt", "hub.key"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}
	if runtime.GOOS != "windows" {
		for _, f := range []string{"ca.key", "hub.key"} {
			st, _ := os.Stat(filepath.Join(dir, f))
			if st.Mode().Perm() != 0o600 {
				t.Errorf("%s mode %o", f, st.Mode().Perm())
			}
		}
	}
	caCrt, caKey, hubCrt := readFile(t, dir+"/ca.crt"), readFile(t, dir+"/ca.key"), readFile(t, dir+"/hub.crt")

	changed, err = EnsureHubPKI(dir, []string{"10.0.0.5", "hub-01.example.com"})
	if err != nil || changed {
		t.Fatalf("second call: changed=%v err=%v", changed, err)
	}
	if string(readFile(t, dir+"/hub.crt")) != string(hubCrt) {
		t.Error("hub.crt changed on identical hosts")
	}

	changed, err = EnsureHubPKI(dir, []string{"hub-01.example.com", "10.0.0.5", "hub-02.example.com"})
	if err != nil || !changed {
		t.Fatalf("third call: changed=%v err=%v", changed, err)
	}
	if string(readFile(t, dir+"/ca.crt")) != string(caCrt) || string(readFile(t, dir+"/ca.key")) != string(caKey) {
		t.Error("CA was replaced")
	}
	ca, signer, err := LoadCA(dir+"/ca.crt", dir+"/ca.key")
	if err != nil || ca == nil || signer == nil {
		t.Fatalf("LoadCA: %v", err)
	}
	c, _, err := loadCertificate(dir + "/hub.crt")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CheckSignatureFrom(ca); err != nil {
		t.Error(err)
	}
	dns := append([]string(nil), c.DNSNames...)
	sort.Strings(dns)
	if len(dns) != 2 || dns[0] != "hub-01.example.com" || dns[1] != "hub-02.example.com" || len(c.IPAddresses) != 1 {
		t.Errorf("SANs dns=%v ips=%v", dns, c.IPAddresses)
	}
}

func TestEnsureHubPKIRequiresHosts(t *testing.T) {
	if _, err := EnsureHubPKI(t.TempDir(), nil); err == nil {
		t.Error("expected error")
	}
}
