package dockercompose

import (
	"encoding/json"
	"os"
	"path"
	"strconv"

	"winterflow/internal/domain/command"
	"winterflow/pkg/crypto"
)

// resolvedItem is a content item after secret resolution: its effective
// plaintext content, keyed by Name (filename or ${VAR} name).
type resolvedItem struct {
	id      string
	name    string
	content []byte
}

// resolveItems turns incoming payload items into plaintext, ready to store and
// render. For each item:
//   - non-encrypted: content used as-is.
//   - encrypted with the "<encrypted>" placeholder: the value from the previous
//     revision is preserved (looked up by name in prevByName).
//   - encrypted otherwise: the ECIES payload is decrypted with the agent key.
//
// Items whose secret can't be resolved (placeholder with no prior value, or a
// decrypt failure) are skipped so a broken secret doesn't poison the render.
func (r *Repository) resolveItems(items []command.ContentItem, prevByName map[string]string) []resolvedItem {
	out := make([]resolvedItem, 0, len(items))
	for _, it := range items {
		name := it.Name
		if name == "" {
			name = it.ID // tolerate older payloads that keyed on ID
		}

		if !it.Encrypted {
			out = append(out, resolvedItem{id: it.ID, name: name, content: it.Content})
			continue
		}

		if string(it.Content) == command.EncryptedPlaceholder {
			if prev, ok := prevByName[name]; ok {
				out = append(out, resolvedItem{id: it.ID, name: name, content: []byte(prev)})
			} else {
				r.log.Warn("encrypted placeholder with no prior value, skipping", "name", name)
			}
			continue
		}

		plaintext, err := crypto.DecryptWithPrivateKey(r.cfg.GetAgentKeyPath(), string(it.Content))
		if err != nil {
			r.log.Warn("failed to decrypt secret, skipping", "name", name, "error", err)
			continue
		}
		out = append(out, resolvedItem{id: it.ID, name: name, content: []byte(plaintext)})
	}
	return out
}

// previousValues loads the stored (plaintext) values from the latest existing
// revision so "<encrypted>" placeholders can preserve unchanged secrets. It
// returns name->value maps for variables and files.
func (r *Repository) previousValues(appID string) (vars map[string]string, files map[string]string) {
	vars = map[string]string{}
	files = map[string]string{}

	revs, err := r.listRevisions(appID)
	if err != nil || len(revs) == 0 {
		return vars, files
	}
	latest := revs[len(revs)-1]
	revDir := path.Join(r.cfg.GetAppsTemplatesDir(), appID, strconv.FormatUint(uint64(latest), 10))

	if raw, err := os.ReadFile(path.Join(revDir, "vars", "values.json")); err == nil {
		var m map[string]string
		if json.Unmarshal(raw, &m) == nil {
			vars = m
		}
	}

	filesDir := path.Join(revDir, "files")
	if entries, err := os.ReadDir(filesDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if content, err := os.ReadFile(path.Join(filesDir, e.Name())); err == nil {
				files[e.Name()] = string(content)
			}
		}
	}
	return vars, files
}
