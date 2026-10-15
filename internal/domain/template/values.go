package template

import (
	"maps"
	"slices"
	"strings"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/compose/xwinterflow"
	"winterflow/pkg/crypto"
)

// Given are the values one call sends for the declared variables: plain
// values by name, and secret ciphertexts (ECIES for the instance's server)
// by name.
type Given struct {
	Values  map[string]string
	Secrets map[string]string
}

// Stored are an instance's recorded values: the plain values its callers
// set (never "") and the names of the secrets it was given.
type Stored struct {
	Values      map[string]string
	SecretNames []string
}

// Values are what a render sends: each plain variable that has a value
// (one without keeps the compose file's own) and, per secret variable that
// has one, the ciphertext or command.EncryptedPlaceholder (the agent keeps
// its stored value).
type Values struct {
	Plain   map[string]string
	Secrets map[string]string
	// Stored is what the instance records once the save is confirmed.
	Stored Stored
}

// Resolve checks a call's values against the declared variables and merges
// them over the stored ones: a given "" clears a stored plain value (the
// compose file's own value stands again); a stored value of a variable the
// revision no longer declares, or that changed between plain and secret,
// is dropped. The error names the variable: undeclared, a secret sent as a
// plain value or the other way round, a plain value with a $ (compose
// would interpolate it from the app's .env), a secret that is not an ECIES
// ciphertext (the agent would commit a plaintext to the app's history);
// then the declared rules (xwinterflow CheckValues: a required one
// missing, a plain value failing its pattern).
func Resolve(spec xwinterflow.Spec, stored Stored, given Given) (Values, error) {
	for _, name := range slices.Sorted(maps.Keys(given.Values)) {
		v, ok := spec.Variable(name)
		switch {
		case !ok:
			return Values{}, Invalid("values."+name, "the template declares no variable %s", name)
		case v.Encrypted:
			return Values{}, Invalid("values."+name, "%s is a secret: send it in secrets, encrypted for the server", name)
		case strings.Contains(given.Values[name], "$"):
			return Values{}, Invalid("values."+name, "may not contain $")
		}
	}
	for _, name := range slices.Sorted(maps.Keys(given.Secrets)) {
		v, ok := spec.Variable(name)
		switch {
		case !ok:
			return Values{}, Invalid("secrets."+name, "the template declares no variable %s", name)
		case !v.Encrypted:
			return Values{}, Invalid("secrets."+name, "%s is not a secret: send it in values", name)
		case given.Secrets[name] == "" || given.Secrets[name] == command.EncryptedPlaceholder:
			return Values{}, Invalid("secrets."+name, "a ciphertext is required (leave the secret out to keep its value)")
		case crypto.CheckCiphertext(given.Secrets[name]) != nil:
			return Values{}, Invalid("secrets."+name, "not a ciphertext: encrypt the value with the server's public key")
		}
	}
	had := map[string]bool{}
	for _, n := range stored.SecretNames {
		had[n] = true
	}
	out := Values{Plain: map[string]string{}, Secrets: map[string]string{}, Stored: Stored{Values: map[string]string{}, SecretNames: []string{}}}
	secret := map[string]bool{}
	for _, v := range spec.Variables {
		if v.Encrypted {
			switch ct, ok := given.Secrets[v.Name]; {
			case ok:
				out.Secrets[v.Name] = ct
			case had[v.Name]:
				out.Secrets[v.Name] = command.EncryptedPlaceholder
			default:
				continue
			}
			secret[v.Name] = true
			out.Stored.SecretNames = append(out.Stored.SecretNames, v.Name)
			continue
		}
		val, set := given.Values[v.Name]
		if !set {
			val = stored.Values[v.Name]
		}
		if val != "" {
			out.Plain[v.Name] = val
			out.Stored.Values[v.Name] = val
		}
	}
	if err := spec.CheckValues(out.Plain, secret); err != nil {
		return Values{}, err
	}
	slices.Sort(out.Stored.SecretNames)
	return out, nil
}
