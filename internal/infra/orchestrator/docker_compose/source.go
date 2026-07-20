package dockercompose

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"

	"winterflow/pkg/crypto"
)

// A git-sourced app keeps its upstream clone under source/ (gitignored from
// winterflow's own history) and records the deployed commit in a committed
// lock file, so rollbacks restore the exact source position too.
const (
	sourceDirRel  = "source"
	sourceLockRel = ".winterflow/source.lock"
)

// sourceSpec is the source configuration carried in the committed config
// blob (minus the token, which lives encrypted in secrets.json).
type sourceSpec struct {
	RepoURL     string
	Branch      string
	ComposePath string
	AutoUpdate  bool
	PollSeconds int
}

// sourceFromConfig extracts the source configuration from a parsed config
// blob; nil when the app is not git-sourced.
func sourceFromConfig(cfg map[string]any) *sourceSpec {
	raw, _ := cfg["source"].(map[string]any)
	if raw == nil {
		return nil
	}
	spec := &sourceSpec{}
	spec.RepoURL, _ = raw["repo_url"].(string)
	spec.Branch, _ = raw["branch"].(string)
	spec.ComposePath, _ = raw["compose_path"].(string)
	spec.AutoUpdate, _ = raw["auto_update"].(bool)
	if v, ok := raw["poll_seconds"].(float64); ok {
		spec.PollSeconds = int(v)
	}
	if spec.RepoURL == "" {
		return nil
	}
	return spec
}

// sourceLock records the deployed upstream commit.
type sourceLock struct {
	SHA string `json:"sha"`
}

func readSourceLock(dir string) (sourceLock, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, sourceLockRel))
	if err != nil {
		return sourceLock{}, false
	}
	var l sourceLock
	if json.Unmarshal(raw, &l) != nil || l.SHA == "" {
		return sourceLock{}, false
	}
	return l, true
}

func writeSourceLock(dir, sha string) error {
	raw, _ := json.Marshal(sourceLock{SHA: sha})
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(sourceLockRel)), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, sourceLockRel), raw, 0o644)
}

// sourceAuth builds transport credentials for http(s) upstreams. Tokens use
// the GitHub-compatible x-access-token convention, which GitLab/Gitea also
// accept.
func sourceAuth(repoURL, token string) *githttp.BasicAuth {
	if token == "" || !strings.HasPrefix(repoURL, "http") {
		return nil
	}
	return &githttp.BasicAuth{Username: "x-access-token", Password: token}
}

// ensureSource makes {dir}/source a checkout of the upstream: cloned on first
// use, fetched after. It checks out `pin` when given (rollback), else the
// branch head, writes the lock, and returns the checked-out SHA.
func (r *Repository) ensureSource(ctx context.Context, dir string, spec sourceSpec, token, pin string) (string, error) {
	srcDir := filepath.Join(dir, sourceDirRel)
	auth := sourceAuth(spec.RepoURL, token)

	repo, err := git.PlainOpen(srcDir)
	if err != nil {
		if _, statErr := os.Stat(srcDir); statErr == nil {
			// A half-cloned or foreign directory: start over.
			if err := os.RemoveAll(srcDir); err != nil {
				return "", err
			}
		}
		repo, err = git.PlainCloneContext(ctx, srcDir, false, &git.CloneOptions{
			URL:  spec.RepoURL,
			Auth: auth,
		})
		if err != nil {
			return "", fmt.Errorf("clone %s: %w", spec.RepoURL, err)
		}
	} else {
		err = repo.FetchContext(ctx, &git.FetchOptions{
			Auth:     auth,
			Force:    true,
			RefSpecs: []config.RefSpec{"+refs/heads/*:refs/remotes/origin/*"},
		})
		if err != nil && err != git.NoErrAlreadyUpToDate {
			return "", fmt.Errorf("fetch %s: %w", spec.RepoURL, err)
		}
	}

	target := pin
	if target == "" {
		rev, err := repo.ResolveRevision(plumbing.Revision("refs/remotes/origin/" + spec.Branch))
		if err != nil {
			return "", fmt.Errorf("resolve branch %q: %w", spec.Branch, err)
		}
		target = rev.String()
	}

	wt, err := repo.Worktree()
	if err != nil {
		return "", err
	}
	if err := wt.Checkout(&git.CheckoutOptions{Hash: plumbing.NewHash(target), Force: true}); err != nil {
		return "", fmt.Errorf("checkout %s: %w", target, err)
	}

	if err := writeSourceLock(dir, target); err != nil {
		return "", err
	}
	return target, nil
}

// sourceTokenPlaintext decrypts the stored repo token; empty when absent or
// undecryptable (anonymous access is attempted then).
func (r *Repository) sourceTokenPlaintext(dir string) string {
	store := loadSecretStore(dir)
	if store.SourceToken == "" {
		return ""
	}
	plaintext, err := crypto.DecryptWithPrivateKey(r.cfg.GetAgentKeyPath(), store.SourceToken)
	if err != nil {
		r.log.Warn("failed to decrypt source token, trying anonymous", "error", err)
		return ""
	}
	return plaintext
}
