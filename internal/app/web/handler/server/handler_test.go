package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-pkgz/auth/v2/token"

	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/service/status"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/repository"
	dbservice "winterflow/internal/infra/db/service"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// newEnv builds a handler over a real (throwaway SQLite) repository with one
// user whose org owns one claimed server.
func newEnv(t *testing.T) (h *Handler, userID, serverID string, cache *status.Cache) {
	t.Helper()
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	conn := db.NewBunConnection(log, "sqlite://"+filepath.Join(t.TempDir(), "test.sqlite"))
	t.Cleanup(func() { _ = conn.Shutdown() })

	userRepo := repository.NewDbUserRepository(conn, log)
	serverRepo := repository.NewDbServerRepository(conn, log)
	users := dbservice.NewDbUserService(log, userRepo)
	servers := dbservice.NewDbServerService(log, serverRepo)

	ctx := context.Background()
	u, err := users.FindOrCreateUser(ctx, dto.UserDTO{Name: "Alice", Provider: "google", AccountID: "g1"})
	if err != nil {
		t.Fatal(err)
	}
	orgID, err := users.PrimaryOrganizationID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Register a pending agent and claim it into the org — the real pairing flow.
	err = serverRepo.RegisterServer(ctx, dto.ServerRegistrationDTO{
		ServerID:             "srv-1",
		CertificateID:        "cert-1",
		Hostname:             "box",
		Code:                 "123456",
		ExpiresAt:            time.Now().Add(time.Hour),
		Certificate:          []byte("PEM"),
		CertificateExpiresAt: time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := serverRepo.ClaimServer(ctx, dto.ClaimServerDTO{Code: "123456", OrganizationID: orgID})
	if err != nil {
		t.Fatal(err)
	}
	if err := serverRepo.SaveCapabilities(ctx, srv.ID, map[string]string{"server_ip": "203.0.113.7"}, nil); err != nil {
		t.Fatal(err)
	}

	cache = status.NewCache(time.Minute)
	h = NewHandler(&Deps{
		Logger:           log,
		ServerService:    servers,
		ServerRepository: serverRepo,
		UserService:      users,
		StatusCache:      cache,
		Cfg:              config.NewServerConfig("standalone"),
	})
	return h, u.ID, srv.ID, cache
}

func authedAs(r *http.Request, userID string) *http.Request {
	return token.SetUserInfo(r, token.User{ID: "p_" + userID, Attributes: map[string]any{"user_id": userID}})
}

func TestGetServersReturnsClaimedServerWithCapabilities(t *testing.T) {
	h, userID, serverID, _ := newEnv(t)

	r := authedAs(httptest.NewRequest("GET", "/api/v1/server/get-servers", nil), userID)
	w := httptest.NewRecorder()
	h.GetServers(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	var body struct {
		Data struct {
			Servers []model.Server `json:"servers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Servers) != 1 || body.Data.Servers[0].ID != serverID {
		t.Fatalf("servers = %+v", body.Data.Servers)
	}
	caps := body.Data.Servers[0].Capabilities
	if len(caps) != 1 || caps[0].Name != "server_ip" || caps[0].Value != "203.0.113.7" {
		t.Fatalf("capabilities = %+v", caps)
	}
}

func TestGetServersStatusReflectsCache(t *testing.T) {
	h, userID, serverID, cache := newEnv(t)

	get := func() map[string]string {
		r := authedAs(httptest.NewRequest("GET", "/api/v1/server/get-servers-status", nil), userID)
		w := httptest.NewRecorder()
		h.GetServersStatus(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
		}
		var body struct {
			Data struct {
				Servers []struct {
					ServerID string `json:"server_id"`
					Liveness string `json:"liveness"`
				} `json:"servers"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, s := range body.Data.Servers {
			out[s.ServerID] = s.Liveness
		}
		return out
	}

	if got := get(); got[serverID] != "unknown" {
		t.Fatalf("before pulse: %v", got)
	}
	cache.MarkOnline(serverID, time.Now())
	if got := get(); got[serverID] != "online" {
		t.Fatalf("after pulse: %v", got)
	}
}

func TestGetServersRequiresAuth(t *testing.T) {
	h, _, _, _ := newEnv(t)
	w := httptest.NewRecorder()
	h.GetServers(w, httptest.NewRequest("GET", "/x", nil))
	if w.Code == http.StatusOK {
		t.Fatalf("unauthenticated request succeeded")
	}
}
