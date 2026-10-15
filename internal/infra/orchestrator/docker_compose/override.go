package dockercompose

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"winterflow/internal/domain/compose/xwinterflow"
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
func (r *Repository) writeComposeOverride(dir string, spec xwinterflow.Spec) error {
	path := filepath.Join(dir, composeOverrideRel)
	var content []byte
	if spec.Declared {
		plain, secrets := envFileValues(dir, envRel), envFileValues(dir, envSecretsRel)
		secretNames := map[string]bool{}
		for name := range secrets {
			secretNames[name] = true
		}
		if err := spec.CheckValues(plain, secretNames); err != nil {
			return err
		}
		if err := spec.CheckSecretValues(secrets); err != nil {
			return err
		}
		ports, err := r.readPorts(dir)
		if err != nil {
			return err
		}
		content, err = xwinterflow.Render(spec,
			func(name string) bool { return plain[name] != "" || secrets[name] != "" },
			func(e xwinterflow.Expose) int { return ports[e.ID()].Host })
		if err != nil {
			return err
		}
	}
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
	return os.WriteFile(path, content, 0o644)
}
