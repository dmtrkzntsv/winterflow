package dockercompose

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"winterflow/internal/domain/command"
	"winterflow/pkg/crypto"
)

// Committed layout inside an app's data dir. Everything except the
// materialized secret outputs (.env.secrets + encrypted file paths, both
// gitignored) is under version control.
const (
	configRel     = ".winterflow/config.json"
	secretsRel    = ".winterflow/secrets.json"
	envRel        = ".env"
	envSecretsRel = ".env.secrets"
	gitignoreRel  = ".gitignore"
	// deployedRel marks the commit that is actually running (written after a
	// successful compose up). Runtime state, not history — gitignored, so
	// drafts (commits ahead of it) don't churn the repo.
	deployedRel = ".winterflow/deployed"
)

// writeDeployedMark records the commit hash that was just deployed.
func writeDeployedMark(dir, sha string) error {
	return os.WriteFile(filepath.Join(dir, deployedRel), []byte(sha), 0o644)
}

// readDeployedMark returns the last successfully deployed commit, ok=false
// when the app has never recorded one (pre-feature apps, fresh drafts).
func readDeployedMark(dir string) (string, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, deployedRel))
	if err != nil {
		return "", false
	}
	sha := strings.TrimSpace(string(raw))
	return sha, sha != ""
}

// secretStore is the committed, encrypted-at-rest secret state: ECIES
// ciphertext (base64) keyed by variable name / file path. Plaintext exists
// only in the gitignored outputs materializeSecrets writes at deploy time.
type secretStore struct {
	Variables map[string]string `json:"variables"`
	Files     map[string]string `json:"files"`
	// SourceToken is the ECIES-encrypted access token for a git-sourced app's
	// private upstream ("" for anonymous). Decrypted only for git transport
	// auth — never materialized into .env.secrets.
	SourceToken string `json:"source_token,omitempty"`
}

func newSecretStore() secretStore {
	return secretStore{Variables: map[string]string{}, Files: map[string]string{}}
}

// appDataDir is the canonical folder for an app: {data}/apps-data/{appID}.
func (r *Repository) appDataDir(appID string) string {
	return filepath.Join(r.cfg.GetAppsDataDir(), filepath.Base(appID))
}

// safeRel validates a payload-supplied relative path: cleaned, non-absolute,
// no traversal outside the app dir, and not touching the managed dotfiles.
func safeRel(name string) (string, error) {
	cleaned := filepath.Clean(name)
	if cleaned == "" || cleaned == "." || filepath.IsAbs(cleaned) ||
		cleaned == ".git" || strings.HasPrefix(cleaned, ".git"+string(filepath.Separator)) ||
		cleaned == ".winterflow" || strings.HasPrefix(cleaned, ".winterflow"+string(filepath.Separator)) ||
		strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || cleaned == ".." {
		return "", fmt.Errorf("invalid file path %q", name)
	}
	return cleaned, nil
}

// loadSecretStore reads the committed secret state; a missing file is an
// empty store.
func loadSecretStore(dir string) secretStore {
	s := newSecretStore()
	raw, err := os.ReadFile(filepath.Join(dir, secretsRel))
	if err != nil {
		return s
	}
	_ = json.Unmarshal(raw, &s)
	if s.Variables == nil {
		s.Variables = map[string]string{}
	}
	if s.Files == nil {
		s.Files = map[string]string{}
	}
	return s
}

// writeAppStore writes the committed state for a save: config.json,
// secrets.json (with "<encrypted>" placeholders resolved by copying the
// PREVIOUS ciphertext), plain files verbatim, .env from plain variables, and
// .gitignore. Plain files managed by the previous config but absent from the
// new one are deleted, as are on-disk copies of files that turned secret.
func (r *Repository) writeAppStore(dir string, app command.AppPayload) (secretStore, error) {
	prev := loadSecretStore(dir)
	next := newSecretStore()

	// Variables: split plain vs secret.
	var plainVars []resolvedItem
	for _, v := range app.Variables {
		name := v.Name
		if name == "" {
			name = v.ID
		}
		if name == managedEnvVar {
			r.log.Warn("dropping reserved variable", "name", name)
			continue
		}
		if !v.Encrypted {
			plainVars = append(plainVars, resolvedItem{name: name, content: v.Content})
			continue
		}
		if string(v.Content) == command.EncryptedPlaceholder {
			if prevCt, ok := prev.Variables[name]; ok {
				next.Variables[name] = prevCt
			} else {
				r.log.Warn("secret variable placeholder with no prior value, skipping", "name", name)
			}
			continue
		}
		next.Variables[name] = string(v.Content)
	}

	// Git-sourced apps: the repo token is a secret with the same placeholder
	// semantics; source/ itself must never enter winterflow's history.
	if app.Source != nil {
		switch string(app.Source.Token) {
		case "":
			// No token (anonymous) — drop any previously stored one only when
			// the client explicitly sent an empty token with a source config.
			next.SourceToken = ""
		case command.EncryptedPlaceholder:
			next.SourceToken = prev.SourceToken
		default:
			next.SourceToken = string(app.Source.Token)
		}
	}

	// Files: plain written verbatim; secret ciphertext recorded.
	type plainFile struct {
		rel     string
		content []byte
	}
	var plainFiles []plainFile
	for _, f := range app.Files {
		name := f.Name
		if name == "" {
			name = f.ID
		}
		rel, err := safeRel(name)
		if err != nil {
			return secretStore{}, err
		}
		if !f.Encrypted {
			plainFiles = append(plainFiles, plainFile{rel: rel, content: f.Content})
			continue
		}
		if string(f.Content) == command.EncryptedPlaceholder {
			if prevCt, ok := prev.Files[rel]; ok {
				next.Files[rel] = prevCt
			} else {
				r.log.Warn("secret file placeholder with no prior value, skipping", "path", rel)
			}
			continue
		}
		next.Files[rel] = string(f.Content)
	}

	// Remove plain files the previous config managed that are gone or turned
	// secret now (a stale committed plaintext copy must not survive).
	keep := map[string]bool{}
	for _, f := range plainFiles {
		keep[f.rel] = true
	}
	for _, old := range r.managedPlainFiles(dir) {
		if !keep[old] {
			if err := os.Remove(filepath.Join(dir, old)); err != nil && !os.IsNotExist(err) {
				return secretStore{}, err
			}
		}
	}
	for rel := range next.Files {
		if !keep[rel] {
			if err := os.Remove(filepath.Join(dir, rel)); err != nil && !os.IsNotExist(err) {
				return secretStore{}, err
			}
		}
	}

	// Write the committed state.
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(configRel)), 0o755); err != nil {
		return secretStore{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, configRel), app.Config, 0o644); err != nil {
		return secretStore{}, err
	}
	secretsJSON, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return secretStore{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, secretsRel), secretsJSON, 0o644); err != nil {
		return secretStore{}, err
	}
	for _, f := range plainFiles {
		dst := filepath.Join(dir, f.rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return secretStore{}, err
		}
		if err := os.WriteFile(dst, f.content, 0o644); err != nil {
			return secretStore{}, err
		}
	}
	// The managed project name makes a bare `docker compose up` in the app
	// dir target the same stack winterflow manages.
	plainVars = append(plainVars, resolvedItem{name: managedEnvVar, content: []byte(projectName(filepath.Base(dir)))})
	if err := os.WriteFile(filepath.Join(dir, envRel), marshalEnv(plainVars), 0o644); err != nil {
		return secretStore{}, err
	}

	ignored := []string{envSecretsRel, deployedRel, runRel}
	if app.Source != nil {
		ignored = append(ignored, sourceDirRel+"/")
	}
	for rel := range next.Files {
		ignored = append(ignored, filepath.ToSlash(rel))
	}
	sort.Strings(ignored)
	if err := os.WriteFile(filepath.Join(dir, gitignoreRel), []byte(strings.Join(ignored, "\n")+"\n"), 0o644); err != nil {
		return secretStore{}, err
	}

	return next, nil
}

// managedPlainFiles lists the plain (non-secret) file paths the CURRENT
// config.json on disk declares — the set writeAppStore managed last save.
func (r *Repository) managedPlainFiles(dir string) []string {
	cfg, _, err := r.readAppConfig(dir)
	if err != nil {
		return nil
	}
	rawFiles, _ := cfg["files"].([]any)
	var out []string
	for _, rf := range rawFiles {
		m, _ := rf.(map[string]any)
		if m == nil {
			continue
		}
		name, _ := m["filename"].(string)
		encrypted, _ := m["is_encrypted"].(bool)
		if name == "" || encrypted {
			continue
		}
		if rel, err := safeRel(name); err == nil {
			out = append(out, rel)
		}
	}
	return out
}

// materializeSecrets decrypts the store with the agent's private key into the
// gitignored deploy outputs: .env.secrets for variables (removed when there
// are none) and each secret file at its path. Undecryptable entries are
// logged and skipped so one broken secret doesn't block a deploy.
func (r *Repository) materializeSecrets(dir string, s secretStore) error {
	var vars []resolvedItem
	for name, ct := range s.Variables {
		plaintext, err := crypto.DecryptWithPrivateKey(r.cfg.GetAgentKeyPath(), ct)
		if err != nil {
			r.log.Warn("failed to decrypt secret variable, skipping", "name", name, "error", err)
			continue
		}
		vars = append(vars, resolvedItem{name: name, content: []byte(plaintext)})
	}
	envSecretsPath := filepath.Join(dir, envSecretsRel)
	if len(vars) == 0 {
		if err := os.Remove(envSecretsPath); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else if err := os.WriteFile(envSecretsPath, marshalEnv(vars), 0o600); err != nil {
		return err
	}

	for rel, ct := range s.Files {
		safe, err := safeRel(rel)
		if err != nil {
			r.log.Warn("invalid secret file path, skipping", "path", rel)
			continue
		}
		plaintext, err := crypto.DecryptWithPrivateKey(r.cfg.GetAgentKeyPath(), ct)
		if err != nil {
			r.log.Warn("failed to decrypt secret file, skipping", "path", safe, "error", err)
			continue
		}
		dst := filepath.Join(dir, safe)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, []byte(plaintext), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// readAppConfig returns the parsed and raw committed config.json.
func (r *Repository) readAppConfig(dir string) (map[string]any, []byte, error) {
	raw, err := os.ReadFile(filepath.Join(dir, configRel))
	if err != nil {
		return nil, nil, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", configRel, err)
	}
	return cfg, raw, nil
}
