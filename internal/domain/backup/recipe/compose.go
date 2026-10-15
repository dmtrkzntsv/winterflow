package recipe

import (
	"errors"
	"fmt"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

// Compose is what backups need from an app's compose files: the top-level
// volume keys, each service's profiles, and the recipe.
type Compose struct {
	Volumes  map[string]bool
	Services map[string][]string // service name -> profiles
	Mounts   map[string][]Mount  // service name -> its volume and bind mounts
	Recipe   []BackupConfig
	// Unparsed is set when a compose file is not valid YAML and was skipped:
	// the recipe is then incomplete, so callers must not conclude that a
	// backup is gone from it (Clean keeps the overrides as they are).
	Unparsed bool
}

// recipeKeys are the keys a recipe entry may have. enabled and storage are
// per-deployment settings: a recipe never turns a backup on.
var recipeKeys = map[string]bool{
	"name": true, "type": true, "volume": true, "service": true, "path": true,
	"paths": true, "exclude": true, "every": true, "keep": true,
}

var keepKeys = map[string]bool{"recent": true, "daily": true, "monthly": true}

// ParseCompose reads the compose files in the order compose loads them (the
// main file, then its override). Volumes and services are merged; recipe
// entries of a later file replace those of the same name. A file that is not
// valid YAML is skipped and sets Unparsed (docker compose reports it at
// deploy); a file with only type errors (profiles: a-string) keeps what
// decoded. The recipe is checked only for its shape here (keys, types, duplicate names);
// Validate checks the values.
func ParseCompose(files ...[]byte) (Compose, error) {
	return ParseComposeNodes(DecodeFiles(files...)...)
}

// DecodeFiles decodes each compose file once into its YAML node tree; a
// file that is not valid YAML is nil.
func DecodeFiles(files ...[]byte) []*yaml.Node {
	out := make([]*yaml.Node, len(files))
	for i, raw := range files {
		var n yaml.Node
		if yaml.Unmarshal(raw, &n) == nil {
			out[i] = &n
		}
	}
	return out
}

// ParseComposeNodes is ParseCompose over files DecodeFiles decoded: the
// x-winterflow parser (internal/domain/compose/xwinterflow) reads the same
// trees, so a compose file is parsed once. A nil tree sets Unparsed.
func ParseComposeNodes(docs ...*yaml.Node) (Compose, error) {
	out := Compose{Volumes: map[string]bool{}, Services: map[string][]string{}, Mounts: map[string][]Mount{}}
	for _, node := range docs {
		if node == nil {
			out.Unparsed = true
			continue
		}
		var doc struct {
			Services map[string]struct {
				Profiles []string    `yaml:"profiles"`
				Volumes  []yaml.Node `yaml:"volumes"`
			} `yaml:"services"`
			Volumes     map[string]any `yaml:"volumes"`
			XWinterflow yaml.Node      `yaml:"x-winterflow"`
		}
		if err := node.Decode(&doc); err != nil {
			var typeErr *yaml.TypeError
			if !errors.As(err, &typeErr) {
				out.Unparsed = true
				continue
			}
		}
		for name := range doc.Volumes {
			out.Volumes[name] = true
		}
		for name, s := range doc.Services {
			out.Services[name] = s.Profiles
			for _, v := range s.Volumes {
				if m, ok := parseMount(&v); ok {
					out.Mounts[name] = addMount(out.Mounts[name], m)
				}
			}
		}
		entries, err := parseRecipe(&doc.XWinterflow)
		if err != nil {
			return Compose{}, err
		}
		for _, e := range entries {
			replaced := false
			for i := range out.Recipe {
				if out.Recipe[i].Name == e.Name {
					out.Recipe[i], replaced = e, true
				}
			}
			if !replaced {
				out.Recipe = append(out.Recipe, e)
			}
		}
	}
	return out, nil
}

// parseRecipe decodes x-winterflow.backups. The other x-winterflow keys are
// the xwinterflow package's.
func parseRecipe(x *yaml.Node) ([]BackupConfig, error) {
	if x.Kind == 0 || x.Tag == "!!null" {
		return nil, nil
	}
	if x.Kind != yaml.MappingNode {
		return nil, invalid("x-winterflow", "must be a mapping")
	}
	var list *yaml.Node
	for i := 0; i+1 < len(x.Content); i += 2 {
		if x.Content[i].Value == "backups" {
			list = x.Content[i+1]
		}
	}
	if list == nil || list.Tag == "!!null" {
		return nil, nil
	}
	if list.Kind != yaml.SequenceNode {
		return nil, invalid("x-winterflow.backups", "must be a list of backups")
	}
	out := make([]BackupConfig, 0, len(list.Content))
	seen := map[string]bool{}
	for i, item := range list.Content {
		field := fmt.Sprintf("x-winterflow.backups[%d]", i)
		if item.Kind != yaml.MappingNode {
			return nil, invalid(field, "must be a mapping with name and type")
		}
		if err := checkKeys(item, recipeKeys, field); err != nil {
			return nil, err
		}
		var c BackupConfig
		if err := item.Decode(&c); err != nil {
			return nil, invalid(field, "%s", yamlError(err))
		}
		if c.Keep != nil {
			for j := 0; j+1 < len(item.Content); j += 2 {
				if item.Content[j].Value == "keep" {
					if err := checkKeys(item.Content[j+1], keepKeys, field+".keep"); err != nil {
						return nil, err
					}
				}
			}
		}
		if err := ValidateName(c.Name); err != nil {
			return nil, invalid(field+".name", "%v", err)
		}
		if seen[c.Name] {
			return nil, invalid(field+".name", "%q is used by another backup", c.Name)
		}
		seen[c.Name] = true
		out = append(out, c)
	}
	return out, nil
}

// checkKeys refuses keys outside allowed in a mapping node.
func checkKeys(m *yaml.Node, allowed map[string]bool, field string) error {
	if m.Kind != yaml.MappingNode {
		return invalid(field, "must be a mapping")
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k := m.Content[i].Value
		if allowed[k] {
			continue
		}
		if k == "enabled" || k == "storage" {
			return invalid(field+"."+k, "a recipe never turns a backup on: enable it and pick its storage in the app's backups")
		}
		return invalid(field+"."+k, "unknown key")
	}
	return nil
}

// yamlError drops the "yaml: unmarshal errors:" preamble.
func yamlError(err error) string {
	msg := strings.TrimPrefix(err.Error(), "yaml: unmarshal errors:")
	return strings.Join(strings.Fields(msg), " ")
}
