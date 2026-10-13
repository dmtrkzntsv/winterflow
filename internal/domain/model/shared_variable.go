package model

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// SharedVariableNameMax is the longest accepted shared variable name (the
// column width).
const SharedVariableNameMax = 255

var sharedVariableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidateSharedVariableName enforces the rule for org- and server-level
// variable names: an env-var identifier that is not reserved.
// COMPOSE_PROJECT_NAME only ever travels as --project-name, and every WF_
// name belongs to the agent's read-only built-ins.
func ValidateSharedVariableName(name string) error {
	switch {
	case name == "":
		return errors.New("name is required")
	case len(name) > SharedVariableNameMax:
		return fmt.Errorf("name is longer than %d characters", SharedVariableNameMax)
	case !sharedVariableName.MatchString(name):
		return fmt.Errorf("invalid name %q: use letters, digits and _, not starting with a digit", name)
	case ReservedVariableName(name):
		return fmt.Errorf("%s is reserved", name)
	}
	return nil
}

// ReservedVariableName reports a name no user variable may take:
// COMPOSE_PROJECT_NAME and the agent's WF_ built-ins.
func ReservedVariableName(name string) bool {
	return name == "COMPOSE_PROJECT_NAME" || strings.HasPrefix(name, "WF_")
}

// Shared variable scopes as reported to clients.
const (
	SharedScopeOrg    = "org"
	SharedScopeServer = "server"
)

// SharedVariable is one org- or server-level variable. A secret carries no
// value here: its per-server ECIES ciphertexts live beside it, and
// CipherServers names the servers that hold one.
type SharedVariable struct {
	ID            string
	OrgID         string
	ServerID      *string // nil = org scope
	Name          string
	IsSecret      bool
	Value         string // plain variables only
	Description   string
	UpdatedAt     time.Time
	UpdatedBy     string
	CipherServers []string
}

// Scope is SharedScopeOrg or SharedScopeServer.
func (v SharedVariable) Scope() string {
	if v.ServerID == nil {
		return SharedScopeOrg
	}
	return SharedScopeServer
}

// SharedVariableSave is one atomic write: the variable row (inserted when
// Create, else value/description/updated_* updated), the ciphertexts to
// upsert (after deleting every stored one when ReplaceCiphertexts, i.e. a
// new value), and the servers whose vars_revision goes up.
type SharedVariableSave struct {
	Variable           SharedVariable
	Create             bool
	Ciphertexts        map[string]string // server_id -> ciphertext
	ReplaceCiphertexts bool              // true: drop every copy, then write Ciphertexts; false: insert only where absent
	Bump               []string
}

// ServerKey is one org server's ECIES public key; PublicKey is "" for a
// server that has never reported one (pending).
type ServerKey struct {
	ServerID  string
	Name      string
	PublicKey string
}

// ServerVarsInfo is what a vars.sync needs to know about its target.
type ServerVarsInfo struct {
	ServerID string
	OrgID    string
	Name     string
	Revision int64
}

var (
	// ErrSharedVariableNotFound is returned for an unknown variable id.
	ErrSharedVariableNotFound = errors.New("shared variable not found")
	// ErrServerNotFound is returned for an unknown server id.
	ErrServerNotFound = errors.New("server not found")
)
