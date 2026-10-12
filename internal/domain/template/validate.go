package template

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/varref"
)

// Limits of a body's parts.
const (
	maxPatternBytes = 512
	maxParameters   = 100
	maxIngressRows  = 50
	maxFileName     = 255
	maxAppName      = 100
	maxDescription  = 1000
)

var (
	// NameRE is a template name.
	NameRE      = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	paramNameRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	// refRE is a ${NAME} reference in app_name, hosts and ports.
	refRE = regexp.MustCompile(`\$\{([^}]*)\}`)
	// placeholder stands in for a parameter without a default when the
	// ingress rows are checked at save: host syntax only.
	placeholder = "wf-placeholder"
)

// reservedFiles are written by the agent; a template can't carry them.
var reservedFiles = map[string]bool{
	".env": true, ".env.secrets": true, ".env.shared": true, ".env.shared.secrets": true, ".gitignore": true,
}

// ValidateName checks a template name.
func ValidateName(name string) error {
	if !NameRE.MatchString(name) {
		return invalid("name", "%q is not a valid template name: lowercase letters, digits and hyphens, starting with a letter, at most 63", name)
	}
	return nil
}

// Validate checks every rule a body must meet at save. The error names the
// field.
func Validate(b Body) error {
	if err := validateFiles(b.Files); err != nil {
		return err
	}
	plain, err := validateParameters(b.Parameters)
	if err != nil {
		return err
	}
	if err := validateAppName(b, plain); err != nil {
		return err
	}
	if len(b.Icon) > 64 || !trimmed(b.Icon) {
		return invalid("icon", "at most 64 characters, no surrounding spaces")
	}
	if len(b.Color) > 32 || !trimmed(b.Color) {
		return invalid("color", "at most 32 characters, no surrounding spaces")
	}
	if err := validateIngress(b, plain); err != nil {
		return err
	}
	return validateBackups(b)
}

func validateFiles(files map[string]string) error {
	if len(model.DetectComposeFiles(func(n string) bool { _, ok := files[n]; return ok })) == 0 {
		return invalid("files", "a compose file is required (%s)", strings.Join(model.ComposeFileNames, ", "))
	}
	for _, name := range sortedKeys(files) {
		if !validFileName(name) {
			return invalid("files", "%q is not a usable file path", name)
		}
	}
	return nil
}

// validFileName mirrors the agent's file path rule (a relative path inside
// the app's folder, not under .git or .winterflow) and refuses the files
// the agent writes itself.
func validFileName(name string) bool {
	if name == "" || len(name) > maxFileName || path.Clean(name) != name || path.IsAbs(name) || strings.Contains(name, `\`) {
		return false
	}
	first, _, _ := strings.Cut(name, "/")
	switch first {
	case ".", "..", ".git", ".winterflow":
		return false
	}
	return !reservedFiles[name]
}

// validateParameters returns the plain parameters by name.
func validateParameters(params []Parameter) (map[string]Parameter, error) {
	if len(params) > maxParameters {
		return nil, invalid("parameters", "at most %d", maxParameters)
	}
	plain := map[string]Parameter{}
	seen := map[string]bool{}
	for i, p := range params {
		field := fmt.Sprintf("parameters[%d]", i)
		switch {
		case !paramNameRE.MatchString(p.Name):
			return nil, invalid(field+".name", "%q is not a valid parameter name: uppercase letters, digits and _, starting with a letter", p.Name)
		case p.Name == "COMPOSE_PROJECT_NAME" || strings.HasPrefix(p.Name, "WF_"):
			return nil, invalid(field+".name", "%s is reserved", p.Name)
		case seen[p.Name]:
			return nil, invalid(field+".name", "%s is declared twice", p.Name)
		case len(p.Description) > maxDescription:
			return nil, invalid(field+".description", "at most %d characters", maxDescription)
		}
		seen[p.Name] = true
		var re *regexp.Regexp
		if p.Pattern != "" {
			if len(p.Pattern) > maxPatternBytes {
				return nil, invalid(field+".pattern", "at most %d bytes", maxPatternBytes)
			}
			var err error
			if re, err = regexp.Compile(p.Pattern); err != nil {
				return nil, invalid(field+".pattern", "%v", err)
			}
		}
		if p.Secret {
			if p.Default != nil {
				return nil, invalid(field+".default", "a secret parameter has no default: a template never holds a secret value")
			}
			continue
		}
		if p.Default != nil && re != nil && !re.MatchString(*p.Default) {
			return nil, invalid(field+".default", "%q does not match the pattern", *p.Default)
		}
		plain[p.Name] = p
	}
	return plain, nil
}

// checkRefs refuses a ${...} in s that is not a declared plain parameter.
func checkRefs(field, s string, plain map[string]Parameter) error {
	for _, m := range refRE.FindAllStringSubmatch(s, -1) {
		if _, ok := plain[m[1]]; !ok {
			return invalid(field, "${%s} is not a plain parameter of the template", m[1])
		}
	}
	return nil
}

func validateAppName(b Body, plain map[string]Parameter) error {
	if strings.TrimSpace(b.AppName) == "" || len(b.AppName) > maxAppName {
		return invalid("app_name", "required, at most %d characters", maxAppName)
	}
	return checkRefs("app_name", b.AppName, plain)
}

// exampleValues are the values the ingress rows are checked with at save:
// each plain parameter's default, or the placeholder.
func exampleValues(plain map[string]Parameter) map[string]string {
	out := map[string]string{}
	for name, p := range plain {
		out[name] = placeholder
		if p.Default != nil && *p.Default != "" {
			out[name] = *p.Default
		}
	}
	return out
}

func validateIngress(b Body, plain map[string]Parameter) error {
	if len(b.Ingress) > maxIngressRows {
		return invalid("ingress", "at most %d rows", maxIngressRows)
	}
	example := exampleValues(plain)
	for i, row := range b.Ingress {
		field := fmt.Sprintf("ingress[%d]", i)
		if err := checkRefs(field+".host", row.Host, plain); err != nil {
			return err
		}
		if err := checkRefs(field+".port", string(row.Port), plain); err != nil {
			return err
		}
		if m := refRE.FindStringSubmatch(string(row.Port)); m == nil {
			if _, ok := canonicalPort(string(row.Port)); !ok {
				return invalid(field+".port", "%q is neither a port 1-65535 nor ${PARAM}", row.Port)
			}
		} else if m[0] != string(row.Port) {
			return invalid(field+".port", "%q: a port is a number or exactly ${PARAM}", row.Port)
		}
		if len(row.Service) > 64 {
			return invalid(field+".service", "at most 64 characters")
		}
	}
	_, err := renderIngress(b.Ingress, example, true)
	return err
}

// validateBackups checks the compose recipe and that every default names a
// backup it declares and is valid applied to it.
func validateBackups(b Body) error {
	comp, err := recipe.ParseCompose(b.composeContents()...)
	if err != nil {
		return invalid("files", "%v", err)
	}
	if err := recipe.Validate(comp); err != nil {
		return invalid("files", "%v", err)
	}
	declared := map[string]bool{}
	for _, r := range comp.Recipe {
		declared[r.Name] = true
	}
	for _, name := range sortedKeys(b.Backups) {
		if !declared[name] {
			return invalid("backups."+name, "the compose file declares no backup %q", name)
		}
		if _, err := recipe.Effective(comp, []recipe.BackupConfig{b.Backups[name].config(name)}); err != nil {
			msg := strings.TrimPrefix(err.Error(), recipe.ErrInvalid.Error()+": backups[0]")
			return fmt.Errorf("%w: backups.%s%s", ErrInvalid, name, msg)
		}
	}
	return nil
}

// config is the backups-list entry of a default.
func (d BackupDefault) config(name string) recipe.BackupConfig {
	return recipe.BackupConfig{Name: name, Enabled: d.Enabled, Storage: d.Storage, Every: d.Every, Keep: d.Keep}
}

// Warnings lists the files' ${VAR} references that nothing provides: not a
// parameter, not a WF_ or COMPOSE_PROJECT_NAME name, not an org shared
// variable (shared reports those), and without a fallback of their own.
func Warnings(b Body, shared func(name string) bool) []string {
	if shared == nil {
		shared = func(string) bool { return false }
	}
	refs := map[string]bool{}
	for _, name := range sortedKeys(b.Files) {
		varref.Undefaulted([]byte(b.Files[name]), refs)
	}
	var out []string
	for _, name := range sortedKeys(refs) {
		if _, ok := b.param(name); ok || strings.HasPrefix(name, "WF_") || name == "COMPOSE_PROJECT_NAME" || shared(name) {
			continue
		}
		out = append(out, fmt.Sprintf("%s is not a parameter; it must come from a shared variable on the server", name))
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
