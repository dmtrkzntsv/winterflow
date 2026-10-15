package template

import (
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/compose/xwinterflow"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/varref"
)

// Limits of a body's parts.
const (
	maxFileName    = 255
	maxSummary     = 200
	maxDescription = 4000
)

// nameRE is a template name.
var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// reservedFiles are written by the agent; a template can't carry them.
var reservedFiles = map[string]bool{
	".env": true, ".env.secrets": true, ".env.shared": true, ".env.shared.secrets": true, ".gitignore": true,
}

// ValidateName checks an org template's name.
func ValidateName(name string) error {
	if !nameRE.MatchString(name) {
		return Invalid("name", "%q is not a valid template name: lowercase letters, digits and hyphens, starting with a letter, at most 63", name)
	}
	return nil
}

// Validate checks every rule a body must meet at save and returns what its
// compose files declare. A body error names the field; an x-winterflow or
// backup recipe error is the shared parser's (it names the key).
func Validate(b Body) (xwinterflow.Spec, error) {
	if err := validateFiles(b.Files); err != nil {
		return xwinterflow.Spec{}, err
	}
	switch {
	case len(b.Summary) > maxSummary || !trimmed(b.Summary) || strings.ContainsAny(b.Summary, "\r\n"):
		return xwinterflow.Spec{}, Invalid("summary", "one line of at most %d characters, no surrounding spaces", maxSummary)
	case len(b.Description) > maxDescription:
		return xwinterflow.Spec{}, Invalid("description", "at most %d characters", maxDescription)
	case len(b.Icon) > 64 || !trimmed(b.Icon):
		return xwinterflow.Spec{}, Invalid("icon", "at most 64 characters, no surrounding spaces")
	case len(b.Color) > 32 || !trimmed(b.Color):
		return xwinterflow.Spec{}, Invalid("color", "at most 32 characters, no surrounding spaces")
	}
	spec, err := Parse(b)
	if err != nil {
		return xwinterflow.Spec{}, err
	}
	if spec.Compose.Unparsed {
		return xwinterflow.Spec{}, Invalid("files", "a compose file is not valid YAML")
	}
	if err := recipe.Validate(spec.Compose); err != nil {
		return xwinterflow.Spec{}, err
	}
	return spec, nil
}

func validateFiles(files map[string]string) error {
	if len(model.DetectComposeFiles(func(n string) bool { _, ok := files[n]; return ok })) == 0 {
		return Invalid("files", "a compose file is required (%s)", strings.Join(model.ComposeFileNames, ", "))
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		if !validFileName(name) {
			return Invalid("files", "%q is not a usable file path", name)
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

// Warnings are what a save should point out without refusing it: the
// parser's (a variable nothing uses), then each ${VAR} the files reference
// that nothing provides: not a declared variable, not a WF_ or
// COMPOSE_PROJECT_NAME name, not an org shared variable (shared reports
// those), and without a fallback of its own. A compose file that is not
// valid YAML (spec.Compose.Unparsed) declares nothing, so the references
// are left out: every one would read as undeclared.
func Warnings(b Body, spec xwinterflow.Spec, shared func(name string) bool) []string {
	if shared == nil {
		shared = func(string) bool { return false }
	}
	out := append([]string{}, spec.Warnings...)
	if spec.Compose.Unparsed {
		return out
	}
	refs := map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(b.Files)) {
		varref.Undefaulted([]byte(b.Files[name]), refs)
	}
	for _, name := range slices.Sorted(maps.Keys(refs)) {
		if _, ok := spec.Variable(name); ok || model.ReservedVariableName(name) || shared(name) {
			continue
		}
		out = append(out, fmt.Sprintf("%s is not a declared variable; it must come from a shared variable on the server", name))
	}
	return out
}
