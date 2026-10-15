package dockercompose

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"winterflow/internal/domain/compose/xwinterflow"
	"winterflow/pkg/fsutil"
)

// composeOverrideRel is the compose override file rendered from the app's
// x-winterflow block at every deploy (xwinterflow.Render): derived state,
// gitignored, passed as the last -f. It holds ${NAME} references, never
// values.
const composeOverrideRel = xwinterflow.OverrideFile

// envFileValues reads one of the app's env files (none when missing).
func envFileValues(dir, rel string) map[string]string {
	raw, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		return map[string]string{}
	}
	return parseEnv(raw)
}

// writeComposeOverride checks the declared variables' values, then writes
// the app's override file, or removes it when the compose files have no
// x-winterflow block or nothing to apply. The API checked the plain values
// at save; only the agent sees the decrypted secrets (.env.secrets, so call
// it after materializeSecrets) and a git-sourced app's compose file: a value
// that breaks its variable fails the deploy, naming the variable. An
// unchanged file is not rewritten.
//
// The file never outlives the compose files it was rendered from: on a
// value error it is written without the variables' lines (so every other
// compose command still reads the current services), on any other error it
// is removed, and the error is returned either way.
func (r *Repository) writeComposeOverride(dir string, spec xwinterflow.Spec) error {
	content, err := r.renderComposeOverride(dir, spec, true)
	if errors.Is(err, xwinterflow.ErrValue) {
		var ferr error
		if content, ferr = r.renderComposeOverride(dir, spec, false); ferr != nil {
			content = nil
		}
	} else if err != nil {
		content = nil
	}
	if werr := putComposeOverride(dir, content); werr != nil && err == nil {
		err = werr
	}
	return err
}

// refreshComposeOverride re-renders the app's override file from the
// compose files just committed (a draft save, a rollback, a source update):
// a deploy writes it again, but every compose command before that (stop,
// restart, logs, the backups' compose config) passes it, and one naming a
// service the compose files no longer have fails them all. Best effort:
// compose files that can't be read remove it; the deploy reports the error.
func (r *Repository) refreshComposeOverride(appID, dir string) {
	spec, err := r.appSpec(dir)
	if err != nil {
		err = putComposeOverride(dir, nil)
	} else {
		err = r.writeComposeOverride(dir, spec)
	}
	if err != nil {
		r.log.Info("compose override file left without what it can't apply", "app_id", appID, "error", err)
	}
}

// renderComposeOverride is the override file's content for spec (nil: no
// file); values checks the declared variables' values first, and without it
// no variable is applied.
func (r *Repository) renderComposeOverride(dir string, spec xwinterflow.Spec, values bool) ([]byte, error) {
	if !spec.Declared {
		return nil, nil
	}
	plain, secrets := envFileValues(dir, envRel), envFileValues(dir, envSecretsRel)
	if values {
		secretNames := map[string]bool{}
		for name := range secrets {
			secretNames[name] = true
		}
		if err := spec.CheckValues(plain, secretNames); err != nil {
			return nil, err
		}
		if err := spec.CheckSecretValues(secrets); err != nil {
			return nil, err
		}
	}
	ports, err := r.readPorts(dir)
	if err != nil {
		return nil, err
	}
	return xwinterflow.Render(spec,
		func(name string) bool { return values && (plain[name] != "" || secrets[name] != "") },
		func(e xwinterflow.Expose) int { return ports[e.ID()].Host })
}

// putComposeOverride writes content as the app's override file (nil:
// removes it), atomically; an unchanged file is not rewritten.
func putComposeOverride(dir string, content []byte) error {
	path := filepath.Join(dir, composeOverrideRel)
	if content == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, content) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fsutil.WriteFile(path, content, 0o644)
}
