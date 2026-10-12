// Package template is a template revision's body: its shape, its
// validation at save, the check of an instance's parameter values, and its
// rendering into an ordinary app.save payload. The agent never sees a
// template; it gets the rendered app.
package template

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/model"
)

// MaxBodyBytes caps a revision's body (its JSON encoding).
const MaxBodyBytes = 1 << 20

// ErrInvalid marks a body, value or request the caller can fix; the
// message names the field (parameters[1].pattern, parameters.NAME). It
// matches model.ErrRejected: the API answers it with a 400.
var ErrInvalid = model.Rejected("invalid template")

func invalid(field, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalid, field, fmt.Sprintf(format, args...))
}

// Body is a revision's body.
type Body struct {
	// Files are the app's files by relative path: what an app save
	// carries, never a secret. A compose file is required.
	Files      map[string]string `json:"files" required:"true" description:"The app's files by relative path (a compose file is required); never a secret."`
	Parameters []Parameter       `json:"parameters" description:"Each becomes an app variable of the same name."`
	// AppName, Icon and Color are the app's catalog record; AppName may
	// use ${PARAM} of plain parameters.
	AppName string       `json:"app_name" required:"true" description:"May use ${PARAM} of plain parameters."`
	Icon    string       `json:"icon"`
	Color   string       `json:"color"`
	Ingress []IngressRow `json:"ingress" description:"Hostname -> host port rows; hosts and ports may use ${PARAM} of plain parameters."`
	// Backups are, per recipe backup name, the defaults an instance gets.
	Backups map[string]BackupDefault `json:"backups" description:"Per recipe backup name, the settings an instance starts with."`
}

// Parameter is one value an instance supplies.
type Parameter struct {
	Name     string `json:"name" required:"true" description:"^[A-Z][A-Z0-9_]*$; not COMPOSE_PROJECT_NAME or WF_*."`
	Secret   bool   `json:"secret,omitempty" description:"Values arrive ECIES-encrypted for the server and are never returned."`
	Required bool   `json:"required,omitempty"`
	// Default is nil when the parameter has none ("" is a default).
	Default     *string `json:"default,omitempty" description:"Plain parameters only; must match pattern."`
	Pattern     string  `json:"pattern,omitempty" description:"Go RE2 regular expression a value must match, at most 512 bytes."`
	Description string  `json:"description,omitempty"`
}

// IngressRow routes Host to the host port Port that the compose file
// publishes (as the app editor's upstream port). Service names the compose
// service behind it, for people; rendering doesn't use it.
type IngressRow struct {
	Host    string `json:"host" required:"true"`
	Service string `json:"service,omitempty"`
	Port    Port   `json:"port" required:"true"`
}

// BackupDefault is an instance's starting settings for one recipe backup.
type BackupDefault struct {
	Enabled bool         `json:"enabled"`
	Storage string       `json:"storage,omitempty" description:"A storage name, looked up on the instance's server."`
	Every   string       `json:"every,omitempty"`
	Keep    *recipe.Keep `json:"keep,omitempty"`
}

// Port is an ingress row's port: a JSON number, or a string that is a
// number or exactly "${PARAM}" of a plain parameter.
type Port string

// UnmarshalJSON accepts a number or a string.
func (p *Port) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*p = Port(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("port must be a number or a string")
	}
	*p = Port(n.String())
	return nil
}

// MarshalJSON writes a number when the port is one.
func (p Port) MarshalJSON() ([]byte, error) {
	if _, err := strconv.Atoi(string(p)); err == nil {
		return []byte(p), nil
	}
	return json.Marshal(string(p))
}

// JSONSchemaBytes documents the two accepted forms (the OpenAPI
// generator reads it; returning bytes keeps this package free of it).
func (Port) JSONSchemaBytes() ([]byte, error) {
	return []byte(`{"type":["integer","string"],"description":"A host port 1-65535, or \"${PARAM}\" of a plain parameter."}`), nil
}

// Parse decodes and validates a body. The error names the field.
func Parse(raw []byte) (Body, error) {
	if len(raw) > MaxBodyBytes {
		return Body{}, invalid("body", "larger than %d bytes", MaxBodyBytes)
	}
	var b Body
	if err := json.Unmarshal(raw, &b); err != nil {
		return Body{}, invalid("body", "%v", err)
	}
	return b, Validate(b)
}

// Encode is the stored form of a validated body.
func Encode(b Body) ([]byte, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxBodyBytes {
		return nil, invalid("body", "larger than %d bytes", MaxBodyBytes)
	}
	return raw, nil
}

// param returns the body's parameter named name.
func (b Body) param(name string) (Parameter, bool) {
	for _, p := range b.Parameters {
		if p.Name == name {
			return p, true
		}
	}
	return Parameter{}, false
}

// composeContents are the body's compose files in compose's load order.
func (b Body) composeContents() [][]byte {
	var out [][]byte
	for _, name := range model.DetectComposeFiles(func(n string) bool { _, ok := b.Files[n]; return ok }) {
		out = append(out, []byte(b.Files[name]))
	}
	return out
}

// RecipeNames are the backup names the body's compose files declare.
func (b Body) RecipeNames() (map[string]bool, error) {
	comp, err := recipe.ParseCompose(b.composeContents()...)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, r := range comp.Recipe {
		out[r.Name] = true
	}
	return out, nil
}

// trimmed reports whether s has no leading or trailing space.
func trimmed(s string) bool { return strings.TrimSpace(s) == s }
