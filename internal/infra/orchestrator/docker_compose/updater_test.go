package dockercompose

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

func newUpdaterTestRepo(t *testing.T) *Repository {
	t.Helper()
	return NewRepository(
		config.NewServerConfig("standalone"),
		logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
	)
}

func TestUpdateAgentNoopWhenNotNewer(t *testing.T) {
	r := newUpdaterTestRepo(t)
	// Default build version is "0.0.0"; an equal/older target is a no-op and must
	// not download or exit.
	res, err := r.UpdateAgent(context.Background(), command.UpdateAgentRequest{Version: "0.0.0"})
	if err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}
	if res.Scheduled {
		t.Error("update should not be scheduled when target is not newer")
	}
}

func TestUpdateAgentRequiresVersion(t *testing.T) {
	r := newUpdaterTestRepo(t)
	if _, err := r.UpdateAgent(context.Background(), command.UpdateAgentRequest{}); err == nil {
		t.Error("expected error for empty target version")
	}
}

func TestUpdateAgentDownloadFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	t.Setenv("GITHUB_RELEASES_URL", srv.URL)

	r := newUpdaterTestRepo(t)
	// Target is newer than the build version ("0.0.0"), so the updater attempts
	// the download; the 404 must fail the update without scheduling an exit.
	res, err := r.UpdateAgent(context.Background(), command.UpdateAgentRequest{Version: "999.0.0"})
	if err == nil {
		t.Fatal("expected error when release download fails")
	}
	if res.Scheduled {
		t.Error("update must not be scheduled on download failure")
	}
}

func TestDownloadBinary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte("new-binary-bytes"))
	}))
	defer srv.Close()

	// The "current executable" whose directory and mode the download must copy.
	dir := t.TempDir()
	execPath := filepath.Join(dir, "winterflow-agent")
	if err := os.WriteFile(execPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	tmpPath, err := downloadBinary(context.Background(), srv.URL+"/release", execPath)
	if err != nil {
		t.Fatalf("downloadBinary: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(tmpPath) })

	if filepath.Dir(tmpPath) != dir {
		t.Errorf("temp file dir = %q, want next to executable %q", filepath.Dir(tmpPath), dir)
	}
	got, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new-binary-bytes" {
		t.Errorf("downloaded content = %q", got)
	}
	info, err := os.Stat(tmpPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("temp file mode = %v, want 0755 copied from executable", info.Mode().Perm())
	}
}

func TestDownloadBinaryNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dir := t.TempDir()
	execPath := filepath.Join(dir, "winterflow-agent")
	if err := os.WriteFile(execPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := downloadBinary(context.Background(), srv.URL+"/missing", execPath); err == nil {
		t.Error("expected error for 404 download")
	}
}

func TestDownloadBinaryStatFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("bytes"))
	}))
	defer srv.Close()

	// Executable path does not exist: stat must fail after the fetch.
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := downloadBinary(context.Background(), srv.URL, missing); err == nil {
		t.Error("expected error when executable cannot be stat'ed")
	}
}
