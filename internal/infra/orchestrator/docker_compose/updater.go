package dockercompose

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/pkg/version"
)

// osExit is indirected so tests can intercept the process exit.
var osExit = os.Exit

// exitDelay is how long the post-update exit waits so the success response can
// travel back through the dispatcher (and, distributed, up the gRPC stream)
// before the process dies. Indirected for tests.
var exitDelay = 2 * time.Second

// UpdateAgent downloads the requested agent release for this OS/arch, replaces
// the running executable, and exits so the process supervisor restarts it on the
// new version. It returns (scheduled=false, nil) when already up to date.
//
// Ported from v1's update_agent handler; v2 stays on the standard library.
func (r *Repository) UpdateAgent(ctx context.Context, in command.UpdateAgentRequest) (command.UpdateAgentResponse, error) {
	current := version.GetVersion()
	resp := command.UpdateAgentResponse{FromVersion: current, ToVersion: in.Version}

	if in.Version == "" {
		return resp, fmt.Errorf("target version is required")
	}
	if runtime.GOOS == "windows" {
		return resp, fmt.Errorf("self-update is not supported on windows")
	}
	// Only update when the target is strictly newer than what we run.
	if !version.IsSmallerThan(in.Version) {
		r.log.Info("agent already up to date", "current", current, "target", in.Version)
		return resp, nil
	}

	execPath, err := os.Executable()
	if err != nil {
		return resp, fmt.Errorf("resolve executable path: %w", err)
	}

	binaryName := fmt.Sprintf("winterflow-agent-%s-%s", runtime.GOOS, runtime.GOARCH)
	url := fmt.Sprintf("%s/%s/%s", r.cfg.GetGitHubReleasesURL(), in.Version, binaryName)
	r.log.Info("downloading agent update", "url", url, "target", in.Version)

	tmpFile, err := downloadBinary(ctx, url, execPath)
	if err != nil {
		return resp, err
	}

	// Atomically replace the current executable (same-filesystem rename).
	if err := os.Rename(tmpFile, execPath); err != nil {
		_ = os.Remove(tmpFile)
		return resp, fmt.Errorf("replace executable: %w", err)
	}

	r.log.Info("agent binary replaced, exiting to restart on new version",
		"from", current, "to", in.Version)
	resp.Scheduled = true

	// Exit after a brief delay so the response can be flushed back to the hub.
	go func() {
		time.Sleep(exitDelay)
		osExit(0)
	}()
	return resp, nil
}

// downloadBinary fetches url into a temp file next to execPath (same filesystem,
// so the later rename is atomic), copying the executable's permission bits.
func downloadBinary(ctx context.Context, url, execPath string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: status %d", httpResp.StatusCode)
	}

	info, err := os.Stat(execPath)
	if err != nil {
		return "", fmt.Errorf("stat executable: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(execPath), ".winterflow-agent-update-*")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := io.Copy(tmp, httpResp.Body); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("write binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, info.Mode()); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("chmod temp file: %w", err)
	}
	return tmpPath, nil
}
