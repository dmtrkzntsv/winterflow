// Package template is a template revision's body: its shape, its
// validation at save, what its compose files declare under x-winterflow
// (the variables an instance fills in, the exposed ports its hostnames
// target, the backups it can turn on), the check of an instance's values,
// and its rendering into an ordinary app.save payload. The agent never sees
// a template; it gets the rendered app, and applies x-winterflow itself.
package template

import (
	"encoding/json"
	"fmt"
	"strings"

	"winterflow/internal/domain/compose/xwinterflow"
	"winterflow/internal/domain/model"
)

// MaxBodyBytes caps a revision's body (its JSON encoding).
const MaxBodyBytes = 1 << 20

// ErrInvalid marks a body, value or request the caller can fix; the
// message names the field (files, values.NAME, hosts[0].expose). It
// matches model.ErrRejected: the API answers it with a 400.
var ErrInvalid = model.Rejected("invalid template")

// Invalid is an ErrInvalid naming field: "invalid template: field: msg".
func Invalid(field, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalid, field, fmt.Sprintf(format, args...))
}

// Body is a revision's body: the app's files and how the template reads in
// a list. What an instance fills in, which ports it exposes and which
// backups it can turn on come from the compose files' x-winterflow block
// (Parse), never from fields of their own.
type Body struct {
	// Files are the app's files by relative path: what an app save
	// carries, never a secret. A compose file is required.
	Files       map[string]string `json:"files" required:"true" description:"The app's files by relative path (a compose file is required); never a secret."`
	Summary     string            `json:"summary" description:"One line about the app, at most 200 characters."`
	Description string            `json:"description" description:"At most 4000 characters."`
	Icon        string            `json:"icon"`
	Color       string            `json:"color"`
}

// Encode is the stored form of a validated body.
func Encode(b Body) ([]byte, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxBodyBytes {
		return nil, Invalid("body", "larger than %d bytes", MaxBodyBytes)
	}
	return raw, nil
}

// composeContents are the body's compose files in compose's load order.
func (b Body) composeContents() [][]byte {
	var out [][]byte
	for _, name := range model.DetectComposeFiles(func(n string) bool { _, ok := b.Files[n]; return ok }) {
		out = append(out, []byte(b.Files[name]))
	}
	return out
}

// Parse reads the body's x-winterflow block with the template rules (a
// volume with a literal name is an error). A stored body passed Validate
// when it was saved; Parse alone is what reading it again needs.
func Parse(b Body) (xwinterflow.Spec, error) {
	return xwinterflow.Parse(xwinterflow.ModeTemplate, b.composeContents()...)
}

// trimmed reports whether s has no leading or trailing space.
func trimmed(s string) bool { return strings.TrimSpace(s) == s }
