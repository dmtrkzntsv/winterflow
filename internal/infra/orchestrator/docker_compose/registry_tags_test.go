package dockercompose

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseImageRef(t *testing.T) {
	cases := []struct {
		in, host, repo string
		wantErr        bool
	}{
		{"nginx", "registry-1.docker.io", "library/nginx", false},
		{"nginx:1.27", "registry-1.docker.io", "library/nginx", false},
		{"grafana/grafana", "registry-1.docker.io", "grafana/grafana", false},
		{"ghcr.io/org/app:v2", "ghcr.io", "org/app", false},
		{"127.0.0.1:5001/foo/bar", "127.0.0.1:5001", "foo/bar", false},
		{"registry.example.com/team/svc@sha256:abcd", "registry.example.com", "team/svc", false},
		{"", "", "", true},
	}
	for _, c := range cases {
		host, repo, err := parseImageRef(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseImageRef(%q): want error", c.in)
			}
			continue
		}
		if err != nil || host != c.host || repo != c.repo {
			t.Errorf("parseImageRef(%q) = %q,%q,%v want %q,%q", c.in, host, repo, err, c.host, c.repo)
		}
	}
}

// fakeRegistry serves /v2/<repo>/tags/list with two pages and optionally a
// bearer-token dance.
func fakeRegistry(t *testing.T, requireBearer bool, sawAuth *[]string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		*sawAuth = append(*sawAuth, "token:"+r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "BEARER-XYZ"})
	})
	mux.HandleFunc("/v2/acme/app/tags/list", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		*sawAuth = append(*sawAuth, "list:"+auth)
		if requireBearer && auth != "Bearer BEARER-XYZ" {
			w.Header().Set("WWW-Authenticate",
				fmt.Sprintf(`Bearer realm=%q,service="reg",scope="repository:acme/app:pull"`, srv.URL+"/token"))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("last") == "" {
			w.Header().Set("Link", `</v2/acme/app/tags/list?last=b2>; rel="next"`)
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "acme/app", "tags": []string{"latest", "1.2"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "acme/app", "tags": []string{"1.10", "0.9"}})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func hostOf(srv *httptest.Server) string {
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestImageTagsAnonymousPaginated(t *testing.T) {
	r := newTestRepo(t)
	var saw []string
	srv := fakeRegistry(t, false, &saw)

	tags, err := r.ImageTags(context.Background(), hostOf(srv)+"/acme/app")
	if err != nil {
		t.Fatal(err)
	}
	// All four tags, "latest" first, numeric descending after.
	want := []string{"latest", "1.10", "1.2", "0.9"}
	if strings.Join(tags, ",") != strings.Join(want, ",") {
		t.Fatalf("tags = %v, want %v", tags, want)
	}
}

func TestImageTagsBearerFlow(t *testing.T) {
	r := newTestRepo(t)
	var saw []string
	srv := fakeRegistry(t, true, &saw)

	tags, err := r.ImageTags(context.Background(), hostOf(srv)+"/acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 4 {
		t.Fatalf("tags = %v", tags)
	}
	joined := strings.Join(saw, "|")
	if !strings.Contains(joined, "token:") || !strings.Contains(joined, "list:Bearer BEARER-XYZ") {
		t.Fatalf("bearer dance not exercised: %v", saw)
	}
}

func TestImageTagsUsesDockerConfigBasicAuth(t *testing.T) {
	r := newTestRepo(t)
	var saw []string
	srv := fakeRegistry(t, false, &saw)

	dockerDir := t.TempDir()
	cred := base64.StdEncoding.EncodeToString([]byte("bob:hunter2"))
	cfg := fmt.Sprintf(`{"auths":{"%s":{"auth":"%s"}}}`, hostOf(srv), cred)
	if err := os.WriteFile(filepath.Join(dockerDir, "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_CONFIG", dockerDir)

	if _, err := r.ImageTags(context.Background(), hostOf(srv)+"/acme/app"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range saw {
		if strings.HasPrefix(s, "list:Basic ") && strings.Contains(s, cred) {
			found = true
		}
	}
	if !found {
		t.Fatalf("basic auth not forwarded: %v", saw)
	}
}

func TestImageTagsUnknownRepoErrors(t *testing.T) {
	r := newTestRepo(t)
	var saw []string
	srv := fakeRegistry(t, false, &saw)
	if _, err := r.ImageTags(context.Background(), hostOf(srv)+"/nope/nothing"); err == nil {
		t.Fatal("expected error for unknown repo")
	}
}
