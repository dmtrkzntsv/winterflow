package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	ingresscaddy "winterflow/internal/infra/ingress/caddy"
	"winterflow/internal/infra/ingress/domains/certtest"
	dockercompose "winterflow/internal/infra/orchestrator/docker_compose"
	"winterflow/internal/infra/transport/grpc/proto"
	"winterflow/pkg/config"
	"winterflow/pkg/crypto/cryptotest"
	"winterflow/pkg/logger"
)

func dispatchJSON(t *testing.T, d *Dispatcher, typ command.Type, req any) *proto.ResponseEnvelope {
	t.Helper()
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return d.Dispatch(context.Background(), envelope(string(typ), payload))
}

func TestDispatchDomainCommands(t *testing.T) {
	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	ing := &fakeIngress{domains: []command.Domain{{RegisteredDomain: model.RegisteredDomain{Name: "*.x.app", TLS: model.DomainTLSOff}, Apps: []string{"a1"}}}}
	d := NewDispatcher(dockercompose.NewRepository(config.NewServerConfig("standalone"), log), ing, log)
	ok := proto.ResponseCode_RESPONSE_CODE_SUCCESS

	resp := dispatchJSON(t, d, command.TypeDomainList, command.ListDomainsRequest{})
	var list command.ListDomainsResponse
	if resp.Base.ResponseCode != ok || json.Unmarshal(resp.Payload, &list) != nil || len(list.Domains) != 1 || list.Domains[0].Apps[0] != "a1" {
		t.Fatalf("domain.list = %+v %s", resp.Base, resp.Payload)
	}
	if ing.reloads != 0 {
		t.Fatal("a read must not reload ingress")
	}

	// The reload's warnings ride along in both mutation responses.
	ing.warnings = []string{"ingress reload failed (previous routing still active): boom"}
	resp = dispatchJSON(t, d, command.TypeDomainCreate, command.CreateDomainRequest{Name: "*.x.app", TLS: model.DomainTLSAuto})
	var created command.CreateDomainResponse
	if resp.Base.ResponseCode != ok || json.Unmarshal(resp.Payload, &created) != nil || created.Domain.Name != "*.x.app" ||
		len(created.Warnings) != 1 || created.Warnings[0] != ing.warnings[0] {
		t.Fatalf("domain.create = %+v %s", resp.Base, resp.Payload)
	}
	resp = dispatchJSON(t, d, command.TypeDomainDelete, command.DeleteDomainRequest{Name: "*.x.app"})
	var deleted command.DeleteDomainResponse
	if resp.Base.ResponseCode != ok || json.Unmarshal(resp.Payload, &deleted) != nil || deleted.Name != "*.x.app" ||
		len(deleted.Warnings) != 1 || deleted.Warnings[0] != ing.warnings[0] {
		t.Fatalf("domain.delete = %+v %s", resp.Base, resp.Payload)
	}
	ing.warnings = nil
	if ing.reloads != 2 {
		t.Fatalf("reloads = %d, want one per successful mutation", ing.reloads)
	}

	// Invalid payloads never reach the manager.
	for typ, req := range map[command.Type]any{
		command.TypeDomainCreate: command.CreateDomainRequest{Name: "../etc", TLS: model.DomainTLSOff},
		command.TypeDomainDelete: command.DeleteDomainRequest{Name: "a/b"},
	} {
		resp := dispatchJSON(t, d, typ, req)
		if resp.Base.ResponseCode == ok || !strings.HasPrefix(resp.Base.Detail, "invalid payload: ") {
			t.Errorf("%s: %+v", typ, resp.Base)
		}
	}
	if len(ing.created) != 1 || len(ing.deleted) != 1 {
		t.Fatalf("manager saw invalid payloads: %+v %+v", ing.created, ing.deleted)
	}

	// A refused mutation is a correlated error and does not reload.
	ing.domainErr = errors.New("*.x.app is used by Shop")
	resp = dispatchJSON(t, d, command.TypeDomainDelete, command.DeleteDomainRequest{Name: "*.x.app"})
	if resp.Base.ResponseCode == ok || resp.Base.Detail != "*.x.app is used by Shop" || resp.RequestId != "req-1" {
		t.Fatalf("refused delete = %+v", resp)
	}
	resp = dispatchJSON(t, d, command.TypeDomainCreate, command.CreateDomainRequest{Name: "y.app", TLS: model.DomainTLSOff})
	if resp.Base.ResponseCode == ok {
		t.Fatal("refused create succeeded")
	}
	if ing.reloads != 2 {
		t.Fatalf("failed mutations reloaded: %d", ing.reloads)
	}
}

func TestDispatchDomainCommandsWithoutIngress(t *testing.T) {
	d := newTestDispatcher(t)
	for typ, req := range map[command.Type]any{
		command.TypeDomainList:   command.ListDomainsRequest{},
		command.TypeDomainCreate: command.CreateDomainRequest{Name: "x.app", TLS: model.DomainTLSOff},
		command.TypeDomainDelete: command.DeleteDomainRequest{Name: "x.app"},
	} {
		if resp := dispatchJSON(t, d, typ, req); resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS ||
			resp.Base.Detail != errNoIngress.Error() {
			t.Errorf("%s without an ingress manager: %+v", typ, resp.Base)
		}
	}
	// The stand-in reloads nothing and reports ingress off.
	if w := d.reloadIngress(context.Background()); w != nil || d.ingress.Enabled() {
		t.Errorf("noIngress: reload %v, enabled %v", w, d.ingress.Enabled())
	}
}

// TestDispatchDomainCreateWithRealManager runs domain.create in certificate
// mode through the real dispatcher and Caddy manager (not started, so no
// ports): the browser-encrypted key is decrypted, checked and stored, and
// domain.list reports the certificate.
func TestDispatchDomainCreateWithRealManager(t *testing.T) {
	dataDir, certDir := t.TempDir(), t.TempDir()
	t.Setenv("AGENT_DATA_DIR", dataDir)
	t.Setenv("HUB_CERT_DIR", certDir)
	t.Setenv("INGRESS_ENABLED", "true")
	pub := cryptotest.AgentKey(t, certDir)
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	cfg := config.NewServerConfig("standalone")
	d := NewDispatcher(dockercompose.NewRepository(cfg, log), ingresscaddy.NewManager(cfg, log), log)

	cert, key := certtest.Issue(t, time.Now().Add(30*24*time.Hour), "*.twillingate.app")
	resp := dispatchJSON(t, d, command.TypeDomainCreate, command.CreateDomainRequest{
		Name: "*.twillingate.app", TLS: model.DomainTLSCertificate,
		Certificate: string(cert), Key: cryptotest.Encrypt(t, pub, string(key)),
	})
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("create: %+v", resp.Base)
	}
	if strings.Contains(string(resp.Payload), "PRIVATE KEY") {
		t.Fatal("the key leaked into the response")
	}
	if keys, _ := filepath.Glob(filepath.Join(dataDir, "domains", "_wildcard_.twillingate.app.*.key")); len(keys) != 1 {
		t.Fatalf("stored keys = %v", keys)
	}
	resp = dispatchJSON(t, d, command.TypeDomainList, command.ListDomainsRequest{})
	var list command.ListDomainsResponse
	if err := json.Unmarshal(resp.Payload, &list); err != nil || len(list.Domains) != 1 ||
		list.Domains[0].NotAfter == nil || list.Domains[0].Issuer != certtest.Issuer {
		t.Fatalf("list = %s (%v)", resp.Payload, err)
	}
}

// servedNotAfter dials the HTTPS listener with SNI host and returns the
// NotAfter of the leaf the server presents, retrying briefly while Caddy
// (re)starts its listeners.
func servedNotAfter(t *testing.T, port int, host string) time.Time {
	t.Helper()
	var lastErr error
	for i := 0; i < 50; i++ {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp",
			fmt.Sprintf("127.0.0.1:%d", port), &tls.Config{
				ServerName:         host,
				InsecureSkipVerify: true, // the test CA is not in the system pool; only the leaf matters
			})
		if err == nil {
			leaf := conn.ConnectionState().PeerCertificates[0]
			_ = conn.Close()
			return leaf.NotAfter
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("tls handshake via caddy never succeeded: %v", lastErr)
	return time.Time{}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// TestDomainCreateReplacingCertificateReloadsCaddy is the renewal regression:
// domain.create replacing a certificate must make the running proxy present
// the new one. Certificate files are named by their content, so the new pair
// changes the generated config and a plain (unforced) reload picks it up.
func TestDomainCreateReplacingCertificateReloadsCaddy(t *testing.T) {
	dataDir, certDir := t.TempDir(), t.TempDir()
	httpPort, httpsPort := freeTCPPort(t), freeTCPPort(t)
	t.Setenv("AGENT_DATA_DIR", dataDir)
	t.Setenv("HUB_CERT_DIR", certDir)
	t.Setenv("INGRESS_ENABLED", "true")
	t.Setenv("INGRESS_HTTP_PORT", fmt.Sprint(httpPort))
	t.Setenv("INGRESS_HTTPS_PORT", fmt.Sprint(httpsPort))
	t.Setenv("LOG_LEVEL", "error")
	pub := cryptotest.AgentKey(t, certDir)
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	cfg := config.NewServerConfig("standalone")
	m := ingresscaddy.NewManager(cfg, log)
	t.Cleanup(m.Stop)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.Start(ctx); err != nil || !m.Enabled() {
		t.Fatalf("start: %v, enabled %v", err, m.Enabled())
	}
	d := NewDispatcher(dockercompose.NewRepository(cfg, log), m, log)

	// Caddy's certificate cache is process-global and survives caddy.Stop,
	// so a fixed name would let a previous run's (-count=N) certificate
	// answer the handshake. A per-run name keeps the assertion honest.
	zone := fmt.Sprintf("r%d.renew.test", time.Now().UnixNano())
	name, host := "*."+zone, "app."+zone
	upload := func(notAfter time.Time) time.Time {
		t.Helper()
		cert, key := certtest.Issue(t, notAfter, name)
		resp := dispatchJSON(t, d, command.TypeDomainCreate, command.CreateDomainRequest{
			Name: name, TLS: model.DomainTLSCertificate,
			Certificate: string(cert), Key: cryptotest.Encrypt(t, pub, string(key)),
		})
		if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
			t.Fatalf("create: %+v", resp.Base)
		}
		return notAfter.UTC().Truncate(time.Second)
	}

	first := upload(time.Now().Add(30 * 24 * time.Hour))
	if got := servedNotAfter(t, httpsPort, host); !got.Equal(first) {
		t.Fatalf("first certificate: served NotAfter %s, want %s", got, first)
	}
	second := upload(time.Now().Add(90 * 24 * time.Hour))
	if got := servedNotAfter(t, httpsPort, host); !got.Equal(second) {
		t.Fatalf("after replacing the certificate Caddy still serves NotAfter %s, want %s (first was %s)", got, second, first)
	}
	t.Logf("served NotAfter: first %s, after replacement %s", first, second)
}
