package dockercompose

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/pkg/crypto"
)

// storageEnvRel holds the values of the WF_STORAGE_ variables an app's
// compose files reference, resolved at every deploy. Derived state with
// plaintext secrets: 0600, gitignored, and excluded from every commit
// whatever .gitignore says (neverCommitted).
const storageEnvRel = ".winterflow/storage.env"

// composeContents reads the app's compose files (missing ones skipped).
func (r *Repository) composeContents(dir string) ([][]byte, error) {
	var out [][]byte
	for _, f := range r.composeFiles(dir, r.appSourceSpec(dir)) {
		raw, err := os.ReadFile(filepath.Join(dir, f))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	return out, nil
}

// storageRefs lists the WF_STORAGE_ / WF_BACKUP_ variables the app's
// compose files reference.
func (r *Repository) storageRefs(dir string) ([]string, error) {
	contents, err := r.composeContents(dir)
	if err != nil {
		return nil, fmt.Errorf("read compose files: %w", err)
	}
	return model.ScanStorageRefs(contents...), nil
}

// materializeStorageEnv writes storage.env with exactly the variables the
// compose files reference — an app never receives credentials for a
// storage it doesn't mention — or removes it when they reference none. A
// reference to a storage this server doesn't have (deleted, or added to a
// git-sourced compose file upstream) fails the deploy.
func (r *Repository) materializeStorageEnv(dir string) error {
	refs, err := r.storageRefs(dir)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, storageEnvRel)
	if len(refs) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	list, err := r.storages.List()
	if err != nil {
		return err
	}
	byName := make(map[string]model.Storage, len(list))
	for _, s := range list {
		byName[s.Name] = s
	}
	parsed, err := model.CheckStorageRefs(refs, func(n string) bool { _, ok := byName[n]; return ok })
	if err != nil {
		return err
	}
	secrets := map[string]string{} // each storage's secret decrypted once
	vars := make([]resolvedItem, 0, len(parsed))
	for _, ref := range parsed {
		s := byName[ref.Storage]
		secret := ""
		if ref.Secret() && s.S3 != nil {
			if _, done := secrets[s.Name]; !done {
				plain, err := crypto.DecryptWithPrivateKey(r.cfg.GetAgentKeyPath(), s.S3.SecretAccessKey)
				if err != nil {
					return fmt.Errorf("decrypt the secret access key of storage %s: %w", s.Name, err)
				}
				secrets[s.Name] = plain
			}
			secret = secrets[s.Name]
		}
		vars = append(vars, resolvedItem{name: ref.Var, content: []byte(model.StorageFieldValue(s, ref.Field, secret))})
	}
	content, err := marshalStorageEnv(vars)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return err
	}
	// WriteFile's perm only applies on create.
	return os.Chmod(path, 0o600)
}

// marshalStorageEnv renders NAME=VALUE lines for docker compose's env-file
// parser, sorted by name. Storage values are operator- or user-supplied
// (a local path, an S3 prefix, a secret), so each is written so that compose
// reads it back verbatim: it can neither add a variable or comment on a new
// line, nor expand $VAR / ${VAR}, nor be cut at a '#'. A value with a line
// break or NUL has no faithful single-line form and is refused outright.
func marshalStorageEnv(vars []resolvedItem) ([]byte, error) {
	sorted := append([]resolvedItem(nil), vars...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].name < sorted[j].name })
	var b bytes.Buffer
	for _, v := range sorted {
		val := string(v.content)
		if strings.ContainsAny(val, "\n\r\x00") {
			return nil, fmt.Errorf("%s contains a line break or NUL character, which cannot be passed to the app", v.name)
		}
		b.WriteString(v.name)
		b.WriteByte('=')
		b.WriteString(quoteEnvValue(val))
		b.WriteByte('\n')
	}
	return b.Bytes(), nil
}

// quoteEnvValue renders a single-line value for a compose env file: bare when
// it only has characters with no special meaning, else single-quoted (taken
// literally, no expansion), else — it holds a single quote or ends in a
// backslash — double-quoted with \, " and $ escaped.
func quoteEnvValue(val string) string {
	bare := true
	for _, c := range val {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_./:@%+,=~-", c)) {
			bare = false
			break
		}
	}
	switch {
	case bare:
		return val
	case !strings.Contains(val, "'") && !strings.HasSuffix(val, `\`):
		// Compose treats a closing quote after a backslash as escaped even
		// inside single quotes, so a trailing backslash needs the other form.
		return "'" + val + "'"
	default:
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`).Replace(val) + `"`
	}
}

// storageVariables describes the referenced WF_STORAGE_ variables for the
// Variables panel: names and storages, never values.
func storageVariables(refs []string) []command.StorageVariable {
	var out []command.StorageVariable
	for _, v := range refs {
		ref, err := model.ParseStorageRef(v)
		if err != nil {
			continue
		}
		out = append(out, command.StorageVariable{Name: ref.Var, Storage: ref.Storage, Secret: ref.Secret()})
	}
	return out
}
