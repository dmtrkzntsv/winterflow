package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	ingresscaddy "winterflow/internal/infra/ingress/caddy"
	"winterflow/internal/infra/ingress/domains/certtest"
	dockercompose "winterflow/internal/infra/orchestrator/docker_compose"
	"winterflow/internal/infra/transport/grpc/proto"
	"winterflow/pkg/config"
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
	ing := &fakeIngress{domains: []command.Domain{{Name: "*.x.app", TLS: command.DomainTLSOff, Apps: []string{"a1"}}}}
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

	resp = dispatchJSON(t, d, command.TypeDomainCreate, command.CreateDomainRequest{Name: "*.x.app", TLS: command.DomainTLSAuto})
	var created command.CreateDomainResponse
	if resp.Base.ResponseCode != ok || json.Unmarshal(resp.Payload, &created) != nil || created.Domain.Name != "*.x.app" {
		t.Fatalf("domain.create = %+v %s", resp.Base, resp.Payload)
	}
	resp = dispatchJSON(t, d, command.TypeDomainDelete, command.DeleteDomainRequest{Name: "*.x.app"})
	if resp.Base.ResponseCode != ok {
		t.Fatalf("domain.delete = %+v", resp.Base)
	}
	if ing.reloads != 2 {
		t.Fatalf("reloads = %d, want one per successful mutation", ing.reloads)
	}

	// Invalid payloads never reach the manager.
	for typ, req := range map[command.Type]any{
		command.TypeDomainCreate: command.CreateDomainRequest{Name: "../etc", TLS: command.DomainTLSOff},
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
	ing.domainErr = errors.New("*.x.app is used by Shop (a1)")
	resp = dispatchJSON(t, d, command.TypeDomainDelete, command.DeleteDomainRequest{Name: "*.x.app"})
	if resp.Base.ResponseCode == ok || resp.Base.Detail != "*.x.app is used by Shop (a1)" || resp.RequestId != "req-1" {
		t.Fatalf("refused delete = %+v", resp)
	}
	resp = dispatchJSON(t, d, command.TypeDomainCreate, command.CreateDomainRequest{Name: "y.app", TLS: command.DomainTLSOff})
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
		command.TypeDomainCreate: command.CreateDomainRequest{Name: "x.app", TLS: command.DomainTLSOff},
		command.TypeDomainDelete: command.DeleteDomainRequest{Name: "x.app"},
	} {
		if resp := dispatchJSON(t, d, typ, req); resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS {
			t.Errorf("%s succeeded without an ingress manager", typ)
		}
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
	pub := certtest.AgentKey(t, certDir)
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	cfg := config.NewServerConfig("standalone")
	d := NewDispatcher(dockercompose.NewRepository(cfg, log), ingresscaddy.NewManager(cfg, log), log)

	cert, key := certtest.Issue(t, time.Now().Add(30*24*time.Hour), "*.twillingate.app")
	resp := dispatchJSON(t, d, command.TypeDomainCreate, command.CreateDomainRequest{
		Name: "*.twillingate.app", TLS: command.DomainTLSCertificate,
		Certificate: string(cert), Key: certtest.Encrypt(t, pub, string(key)),
	})
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("create: %+v", resp.Base)
	}
	if strings.Contains(string(resp.Payload), "PRIVATE KEY") {
		t.Fatal("the key leaked into the response")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "domains", "_wildcard_.twillingate.app.key")); err != nil {
		t.Fatal(err)
	}
	resp = dispatchJSON(t, d, command.TypeDomainList, command.ListDomainsRequest{})
	var list command.ListDomainsResponse
	if err := json.Unmarshal(resp.Payload, &list); err != nil || len(list.Domains) != 1 ||
		list.Domains[0].NotAfter == nil || list.Domains[0].Issuer != certtest.Issuer {
		t.Fatalf("list = %s (%v)", resp.Payload, err)
	}
}
