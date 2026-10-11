package dockercompose

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/pkg/crypto"
)

// storageEnvRel holds the values of the WF_STORAGE_ variables an app's
// compose files reference, resolved at every deploy. Derived state with
// plaintext secrets: 0600, gitignored, and excluded from every commit
// whatever .gitignore says (neverCommitted).
const storageEnvRel = ".winterflow/storage.env"

// storageFolderTimeout bounds the deploy-time check that a referenced local
// storage's folder exists (a variable so tests can shrink it).
var storageFolderTimeout = 10 * time.Second

// statStorageFolder stats a local storage's folder; replaced in tests.
var statStorageFolder = os.Stat

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
// git-sourced compose file upstream) fails the deploy, and so does a
// referenced local storage whose folder is missing (see checkStorageFolder).
func (r *Repository) materializeStorageEnv(ctx context.Context, dir string) error {
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
	checked := map[string]bool{}   // each local folder checked once
	vars := make([]resolvedItem, 0, len(parsed))
	for _, ref := range parsed {
		s := byName[ref.Storage]
		if s.Type == model.StorageLocal && s.Local != nil && !checked[s.Name] {
			checked[s.Name] = true
			if err := checkStorageFolder(ctx, s); err != nil {
				return err
			}
		}
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

// checkStorageFolder refuses a deploy whose compose file references a
// local storage whose folder is missing: docker would create a missing bind
// source as an empty folder on the system disk (the unmounted USB disk case)
// and the app would fill it. The stat runs in a goroutine abandoned after
// storageFolderTimeout, like probeStorage: a hung NFS/CIFS mount must not
// wedge the (standalone: single) command pipeline.
func checkStorageFolder(parent context.Context, s model.Storage) error {
	ctx, cancel := context.WithTimeout(parent, storageFolderTimeout)
	defer cancel()
	folder := s.Local.Path
	stat := statStorageFolder
	done := make(chan error, 1) // buffered: an abandoned stat must not block on send
	go func() {
		info, err := stat(folder)
		if err == nil && !info.IsDir() {
			err = errors.New("it is not a folder")
		}
		done <- err
	}()
	select {
	case err := <-done:
		switch {
		case err == nil:
			return nil
		case errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("storage %s: the folder %s is missing (is the disk mounted?): not deploying, so docker does not create it on the system disk", s.Name, folder)
		default:
			return fmt.Errorf("storage %s: cannot use the folder %s: %w", s.Name, folder, err)
		}
	case <-ctx.Done():
		if err := parent.Err(); err != nil {
			return fmt.Errorf("storage %s: the folder %s was not checked: %w", s.Name, folder, err)
		}
		return fmt.Errorf("storage %s: the folder %s did not answer within %s (is the share reachable?)", s.Name, folder, storageFolderTimeout)
	}
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
