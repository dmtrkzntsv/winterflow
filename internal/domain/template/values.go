package template

import (
	"regexp"
	"sort"
	"strings"

	"winterflow/internal/domain/command"
	"winterflow/pkg/crypto"
)

// Given are the values one call sends: plain values by name, and secret
// ciphertexts (ECIES for the instance's server) by name.
type Given struct {
	Params  map[string]string
	Secrets map[string]string
}

// Stored are an instance's recorded values: the plain values its callers
// set (not the defaults) and the names of the secrets it was given.
type Stored struct {
	Params      map[string]string
	SecretNames []string
}

// Values are what a render uses: every plain parameter's value (set, or
// its default; "" when it has neither) and, per secret parameter that has
// one, the ciphertext or command.EncryptedPlaceholder (the agent keeps its
// stored value).
type Values struct {
	Plain   map[string]string
	Secrets map[string]string
	// Stored is what the instance records once the save is confirmed.
	Stored Stored
}

// Resolve checks a call's values against the body's parameters and merges
// them over the stored ones (a stored value of a parameter the body no
// longer has, or that changed between plain and secret, is dropped). The
// error names the parameter: unknown, a secret sent as a plain value or the
// other way round, a required one missing, a value that doesn't match its
// pattern, a plain value with a $ (compose would interpolate it from the
// app's .env), a secret that is not an ECIES ciphertext (the agent would
// commit a plaintext to the app's history).
func Resolve(b Body, stored Stored, given Given) (Values, error) {
	for _, name := range sortedKeys(given.Params) {
		p, ok := b.param(name)
		switch {
		case !ok:
			return Values{}, invalid("parameters."+name, "the template has no parameter %s", name)
		case p.Secret:
			return Values{}, invalid("parameters."+name, "%s is a secret: send it in secrets, encrypted for the server", name)
		case strings.Contains(given.Params[name], "$"):
			return Values{}, invalid("parameters."+name, "may not contain $")
		}
	}
	for _, name := range sortedKeys(given.Secrets) {
		p, ok := b.param(name)
		switch {
		case !ok:
			return Values{}, invalid("secrets."+name, "the template has no parameter %s", name)
		case !p.Secret:
			return Values{}, invalid("secrets."+name, "%s is not a secret parameter: send it in parameters", name)
		case given.Secrets[name] == "" || given.Secrets[name] == command.EncryptedPlaceholder:
			return Values{}, invalid("secrets."+name, "a ciphertext is required (leave the secret out to keep its value)")
		case crypto.CheckCiphertext(given.Secrets[name]) != nil:
			return Values{}, invalid("secrets."+name, "not a ciphertext: encrypt the value with the server's public key")
		}
	}
	had := map[string]bool{}
	for _, n := range stored.SecretNames {
		had[n] = true
	}
	out := Values{Plain: map[string]string{}, Secrets: map[string]string{}, Stored: Stored{Params: map[string]string{}}}
	for _, p := range b.Parameters {
		if p.Secret {
			switch ct, ok := given.Secrets[p.Name]; {
			case ok:
				out.Secrets[p.Name] = ct
			case had[p.Name]:
				out.Secrets[p.Name] = command.EncryptedPlaceholder
			case p.Required:
				return Values{}, invalid("secrets."+p.Name, "required")
			default:
				continue
			}
			out.Stored.SecretNames = append(out.Stored.SecretNames, p.Name)
			continue
		}
		v, set := given.Params[p.Name]
		if !set {
			v, set = stored.Params[p.Name]
		}
		if set {
			out.Stored.Params[p.Name] = v
		} else if p.Default != nil {
			v = *p.Default
		}
		if p.Required && v == "" {
			return Values{}, invalid("parameters."+p.Name, "required")
		}
		if set && p.Pattern != "" {
			re, err := regexp.Compile(p.Pattern)
			if err != nil || !re.MatchString(v) {
				return Values{}, invalid("parameters."+p.Name, "%q does not match the pattern %s", v, p.Pattern)
			}
		}
		out.Plain[p.Name] = v
	}
	sort.Strings(out.Stored.SecretNames)
	return out, nil
}
