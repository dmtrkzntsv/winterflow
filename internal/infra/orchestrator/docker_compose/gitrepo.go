package dockercompose

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// commitInfo is one entry of an app's git history, as reported to the API.
type commitInfo struct {
	Hash      string `json:"hash"`
	Subject   string `json:"subject"`
	Timestamp int64  `json:"timestamp"` // unix seconds
}

// gitAuthor is the fixed identity for agent-made commits. Apps repos are
// machine-managed; there is no meaningful per-user identity on the agent.
var gitAuthor = &object.Signature{Name: "WinterFlow Agent", Email: "agent@winterflow.local"}

// gitEnsure initializes a repository in dir if none exists yet.
func gitEnsure(dir string) error {
	_, err := git.PlainOpen(dir)
	if err == nil {
		return nil
	}
	if !errors.Is(err, git.ErrRepositoryNotExists) {
		return err
	}
	_, err = git.PlainInit(dir, false)
	return err
}

// gitCommitAll stages everything (honoring .gitignore) and commits. A clean
// worktree is a no-op that returns the current HEAD hash, so callers can
// commit unconditionally after a save.
func gitCommitAll(dir, subject string) (string, error) {
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return "", err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return "", err
	}
	if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return "", err
	}
	status, err := wt.Status()
	if err != nil {
		return "", err
	}
	if status.IsClean() {
		head, err := repo.Head()
		if err != nil {
			return "", fmt.Errorf("nothing to commit and no HEAD: %w", err)
		}
		return head.Hash().String(), nil
	}
	sig := *gitAuthor
	sig.When = time.Now()
	hash, err := wt.Commit(subject, &git.CommitOptions{Author: &sig})
	if err != nil {
		return "", err
	}
	return hash.String(), nil
}

// gitLog returns the app's history, newest first.
func gitLog(dir string) ([]commitInfo, error) {
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return nil, err
	}
	head, err := repo.Head()
	if err != nil {
		return nil, err
	}
	iter, err := repo.Log(&git.LogOptions{From: head.Hash()})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	var out []commitInfo
	err = iter.ForEach(func(c *object.Commit) error {
		out = append(out, commitInfo{
			Hash:      c.Hash.String(),
			Subject:   firstLine(c.Message),
			Timestamp: c.Author.When.Unix(),
		})
		return nil
	})
	return out, err
}

// gitCount returns the number of commits reachable from HEAD, walking without
// materializing the log.
func gitCount(dir string) (int, error) {
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return 0, err
	}
	head, err := repo.Head()
	if err != nil {
		return 0, err
	}
	iter, err := repo.Log(&git.LogOptions{From: head.Hash()})
	if err != nil {
		return 0, err
	}
	defer iter.Close()
	count := 0
	err = iter.ForEach(func(*object.Commit) error {
		count++
		return nil
	})
	return count, err
}

// commitCount is gitCount behind the per-app HEAD-keyed cache.
func (r *Repository) commitCount(appID, dir string) (int, error) {
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return 0, err
	}
	head, err := repo.Head()
	if err != nil {
		return 0, err
	}
	h := head.Hash().String()

	r.versionMu.Lock()
	if e, ok := r.versionCache[appID]; ok && e.head == h {
		r.versionMu.Unlock()
		return e.count, nil
	}
	r.versionMu.Unlock()

	count, err := gitCount(dir)
	if err != nil {
		return 0, err
	}
	r.versionMu.Lock()
	r.versionCache[appID] = versionEntry{head: h, count: count}
	r.versionMu.Unlock()
	return count, nil
}

// gitRestore makes the worktree match the tree of the given commit: files in
// the target tree are (re)written, and files tracked at HEAD but absent from
// the target are deleted. Untracked/ignored files (e.g. .env.secrets) are left
// alone. It does NOT commit — the caller commits the restored tree so history
// stays linear.
func gitRestore(dir, hash string) error {
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return err
	}
	target, err := repo.CommitObject(plumbing.NewHash(hash))
	if err != nil {
		return fmt.Errorf("resolve commit %s: %w", hash, err)
	}
	targetTree, err := target.Tree()
	if err != nil {
		return err
	}

	targetFiles := map[string]struct{}{}
	if err := targetTree.Files().ForEach(func(f *object.File) error {
		targetFiles[f.Name] = struct{}{}
		return nil
	}); err != nil {
		return err
	}

	// Delete files tracked at HEAD that the target tree doesn't have.
	if head, err := repo.Head(); err == nil {
		if headCommit, err := repo.CommitObject(head.Hash()); err == nil {
			if headTree, err := headCommit.Tree(); err == nil {
				if err := headTree.Files().ForEach(func(f *object.File) error {
					if _, keep := targetFiles[f.Name]; !keep {
						if err := os.Remove(filepath.Join(dir, f.Name)); err != nil && !os.IsNotExist(err) {
							return err
						}
					}
					return nil
				}); err != nil {
					return err
				}
			}
		}
	}

	// Write the target tree's files.
	return targetTree.Files().ForEach(func(f *object.File) error {
		content, err := f.Contents()
		if err != nil {
			return err
		}
		dst := filepath.Join(dir, f.Name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if f.Mode == filemode.Executable {
			mode = 0o755
		}
		return os.WriteFile(dst, []byte(content), mode)
	})
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}
