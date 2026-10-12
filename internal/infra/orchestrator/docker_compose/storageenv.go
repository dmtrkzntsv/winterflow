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

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

// storageEnvRel holds the values of the WF_STORAGE_ variables an app's
// compose files reference, resolved at every deploy. Derived state with
// plaintext secrets: 0600, gitignored, and excluded from every commit
// whatever .gitignore says (neverCommitted).
const storageEnvRel = ".winterflow/storage.env"

// composeContents reads the given compose files of the app in dir (missing
// ones skipped).
func composeContents(dir string, files []string) ([][]byte, error) {
	var out [][]byte
	for _, f := range files {
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
func (r *Repository) storageRefs(dir string, files []string) ([]string, error) {
	contents, err := composeContents(dir, files)
	if err != nil {
		return nil, fmt.Errorf("read compose files: %w", err)
	}
	return model.ScanStorageRefs(contents...), nil
}

// materializeStorageEnv writes storage.env with exactly the variables the
// compose files reference — an app never receives credentials for a
// storage it doesn't mention — or removes it when they reference none. A
// WF_BACKUP_ variable takes its value from the storage of one of the app's
// backups (backups), or is empty while that backup is off. A reference to a
// storage this server doesn't have (deleted, or added to a git-sourced
// compose file upstream) or to a backup the app doesn't declare fails the
// deploy, and so does a referenced local storage whose folder is missing
// (see checkStorageFolders). An unchanged storage.env is not rewritten.
func (r *Repository) materializeStorageEnv(ctx context.Context, dir string, files []string, backups []recipe.EffectiveBackup) error {
	content, err := r.renderStorageEnv(ctx, dir, files, backups, true)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, storageEnvRel)
	if content == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if old, err := os.ReadFile(path); err != nil || !bytes.Equal(old, content) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			return err
		}
	}
	// WriteFile's perm only applies on create.
	return os.Chmod(path, 0o600)
}

// renderStorageEnv is the content materializeStorageEnv writes (nil: no
// references). checkFolders also checks the referenced local storages'
// folders.
func (r *Repository) renderStorageEnv(ctx context.Context, dir string, files []string, backups []recipe.EffectiveBackup, checkFolders bool) ([]byte, error) {
	refs, err := r.storageRefs(dir, files)
	if err != nil || len(refs) == 0 {
		return nil, err
	}
	declared := recipe.Declared(backups)
	// Only the referenced registrations are read.
	byName := map[string]model.Storage{}
	var readErr error
	lookup := func(name string) bool {
		if _, ok := byName[name]; ok {
			return true
		}
		s, ok, err := r.storages.Get(name)
		if err != nil && readErr == nil {
			readErr = err
		}
		if ok {
			byName[name] = s
		}
		return ok
	}
	parsed, err := model.CheckStorageRefs(refs, lookup, func(name string) bool { _, ok := declared[name]; return ok })
	if readErr != nil {
		return nil, readErr
	}
	if err != nil {
		return nil, err
	}
	// A backup that is on resolves to its storage, which must exist.
	for _, ref := range parsed {
		if b := declared[ref.Backup]; ref.Backup != "" && b.Enabled && !lookup(b.Storage) {
			if readErr != nil {
				return nil, readErr
			}
			return nil, fmt.Errorf("%w: ${%s}: the backup %s uses the storage %q, which this server doesn't have", model.ErrStorageRef, ref.Var, b.Name, b.Storage)
		}
	}
	if checkFolders {
		var folders []model.Storage
		for _, s := range byName {
			if s.Type == model.StorageLocal && s.Local != nil {
				folders = append(folders, s)
			}
		}
		sort.Slice(folders, func(i, j int) bool { return folders[i].Name < folders[j].Name })
		if err := r.checkStorageFolders(ctx, folders); err != nil {
			return nil, err
		}
	}
	appID := filepath.Base(dir)
	secrets := map[string]string{} // each storage's secret decrypted once
	vars := make([]resolvedItem, 0, len(parsed))
	for _, ref := range parsed {
		name, b := ref.Storage, declared[ref.Backup]
		if ref.Backup != "" {
			if !b.Enabled {
				vars = append(vars, resolvedItem{name: ref.Var}) // off: empty
				continue
			}
			name = b.Storage
		}
		s := byName[name]
		secret := ""
		if ref.Secret() && s.S3 != nil {
			if _, done := secrets[s.Name]; !done {
				if secrets[s.Name], err = r.storageSecret(s); err != nil {
					return nil, err
				}
			}
			secret = secrets[s.Name]
		}
		value := model.StorageFieldValue(s, ref.Field, secret)
		if ref.Backup != "" {
			value = model.BackupFieldValue(s, ref.Field, secret, appID, ref.Backup)
		}
		vars = append(vars, resolvedItem{name: ref.Var, content: []byte(value)})
	}
	return marshalStorageEnv(vars)
}

// checkStorageFolders refuses a deploy whose compose file references a
// local storage whose folder is missing: docker would create a missing bind
// source as an empty folder on the system disk (the unmounted USB disk case)
// and the app would fill it. The folders are checked concurrently, each
// opened the way a backup opens it, in goroutines abandoned (see detached)
// after one shared folder limit: a hung NFS/CIFS mount must not wedge the
// (standalone: single) command pipeline. The first folder in name order
// that fails is reported.
func (r *Repository) checkStorageFolders(parent context.Context, folders []model.Storage) error {
	if len(folders) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, r.storageLimits.folder)
	defer cancel()
	results := make([]<-chan error, len(folders))
	for i, s := range folders {
		results[i] = detached(func() error { _, err := r.openStorage(s, ""); return err })
	}
	for i, s := range folders {
		folder := s.Local.Path
		select {
		case err := <-results[i]:
			switch {
			case err == nil:
			case errors.Is(err, fs.ErrNotExist):
				return fmt.Errorf("storage %s: the folder %s is missing (is the disk mounted?): not deploying, so docker does not create it on the system disk", s.Name, folder)
			default:
				return fmt.Errorf("storage %s: cannot use the folder %s: %w", s.Name, folder, err)
			}
		case <-ctx.Done():
			if err := parent.Err(); err != nil {
				return fmt.Errorf("storage %s: the folder %s was not checked: %w", s.Name, folder, err)
			}
			return fmt.Errorf("storage %s: the folder %s did not answer within %s (is the share reachable?)", s.Name, folder, r.storageLimits.folder)
		}
	}
	return nil
}

// marshalStorageEnv renders NAME=VALUE lines for docker compose's env-file
// parser, sorted by name. Storage values are operator- or user-supplied
// (a local path, an S3 prefix, a secret), so each is written so that compose
// reads it back verbatim: it can neither add a variable or comment on a new
// line, nor expand $VAR / ${VAR}, nor be cut at a '#'. A value with a line
// break or NUL has no faithful single-line form and is refused outright.
func marshalStorageEnv(vars []resolvedItem) ([]byte, error) {
	return writeEnvLines(vars, func(name, val string) (string, error) {
		if strings.ContainsAny(val, "\n\r\x00") {
			return "", fmt.Errorf("%s contains a line break or NUL character, which cannot be passed to the app", name)
		}
		return quoteEnvValue(val), nil
	})
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

// storageVariables describes the referenced WF_STORAGE_ and WF_BACKUP_
// variables for the Variables panel: names and storages (a backup's: the
// one it uses while on), never values.
func storageVariables(refs []string, backups []recipe.EffectiveBackup) []command.StorageVariable {
	declared := recipe.Declared(backups)
	var out []command.StorageVariable
	for _, v := range refs {
		ref, err := model.ParseStorageRef(v)
		if err != nil {
			continue
		}
		storage := ref.Storage
		if b, ok := declared[ref.Backup]; ok && b.Enabled {
			storage = b.Storage
		}
		out = append(out, command.StorageVariable{Name: ref.Var, Storage: storage, Backup: ref.Backup, Secret: ref.Secret()})
	}
	return out
}
