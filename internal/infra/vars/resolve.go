package vars

import (
	"sort"

	"winterflow/internal/domain/command"
)

// Value scopes, lowest precedence first.
const (
	ScopeBuiltin = "builtin"
	ScopeOrg     = "org"
	ScopeServer  = "server"
)

// Value is one resolved variable.
type Value struct {
	Value  string
	Secret bool
	Scope  string
}

// Decrypter turns one ECIES ciphertext into plaintext with the agent key.
type Decrypter func(ciphertext string) (string, error)

// Resolution is a snapshot resolved for this server: the effective value
// of every name, the secrets that did not decrypt, and the secrets this
// server has no copy of.
type Resolution struct {
	Values        map[string]Value
	Undecryptable []command.VarRef
	Missing       []command.VarRef
}

// Resolve applies built-ins < org < server: a later scope replaces an
// earlier entry of the same name, whether either is plain or secret. A
// secret without a ciphertext (Missing) or one that does not decrypt
// (Undecryptable) is left out, as if its scope did not define it.
func Resolve(snap command.SyncVarsRequest, builtins map[string]string, decrypt Decrypter) Resolution {
	res := Resolution{Values: map[string]Value{}}
	for name, v := range builtins {
		res.Values[name] = Value{Value: v, Scope: ScopeBuiltin}
	}
	apply := func(scope string, list []command.SharedVar) {
		for _, v := range list {
			if !v.IsSecret {
				res.Values[v.Name] = Value{Value: v.Value, Scope: scope}
				continue
			}
			if v.Ciphertext == "" {
				res.Missing = append(res.Missing, command.VarRef{Scope: scope, Name: v.Name})
				continue
			}
			plain, err := decrypt(v.Ciphertext)
			if err != nil {
				res.Undecryptable = append(res.Undecryptable, command.VarRef{Scope: scope, Name: v.Name})
				continue
			}
			res.Values[v.Name] = Value{Value: plain, Secret: true, Scope: scope}
		}
	}
	apply(ScopeOrg, snap.Org)
	apply(ScopeServer, snap.Server)
	sortRefs(res.Undecryptable)
	sortRefs(res.Missing)
	return res
}

func sortRefs(refs []command.VarRef) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Name != refs[j].Name {
			return refs[i].Name < refs[j].Name
		}
		return refs[i].Scope < refs[j].Scope
	})
}

// ServerBuiltins are the read-only WF_ values every app on the server
// shares.
func ServerBuiltins(snap command.SyncVarsRequest, dataDir, tz string) map[string]string {
	return map[string]string{
		"WF_SERVER_ID":   snap.ServerID,
		"WF_SERVER_NAME": snap.ServerName,
		"WF_ORG_ID":      snap.OrgID,
		"WF_DATA_DIR":    dataDir,
		"WF_TZ":          tz,
	}
}

// AppBuiltins are the read-only WF_ values specific to one app.
func AppBuiltins(appID, slug, appDir string) map[string]string {
	return map[string]string{
		"WF_APP_ID":   appID,
		"WF_APP_SLUG": slug,
		"WF_APP_DIR":  appDir,
	}
}
