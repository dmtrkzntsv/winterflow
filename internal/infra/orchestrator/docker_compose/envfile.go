package dockercompose

import (
	"bytes"
	"sort"
	"strings"
)

// resolvedItem is a named plaintext value (a variable or file content) ready
// to serialize into a deploy artifact.
type resolvedItem struct {
	name    string
	content []byte
}

// marshalEnv renders variables as a compose-compatible env file: NAME=VALUE
// lines sorted by name. Values containing newlines or double quotes are
// double-quoted with \n and \" escapes (the dotenv dialect docker compose
// reads); plain values stay unquoted so the file diffs nicely in git.
func marshalEnv(vars []resolvedItem) []byte {
	sorted := append([]resolvedItem(nil), vars...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].name < sorted[j].name })

	var b bytes.Buffer
	for _, v := range sorted {
		val := string(v.content)
		if strings.ContainsAny(val, "\n\"") {
			val = "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n").Replace(val) + "\""
		}
		b.WriteString(v.name)
		b.WriteByte('=')
		b.WriteString(val)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// parseEnv is marshalEnv's inverse: NAME=VALUE lines into a map, tolerating
// comments and blank lines. Double-quoted values are unescaped.
func parseEnv(raw []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, val, ok := strings.Cut(line, "=")
		if !ok || name == "" {
			continue
		}
		if len(val) >= 2 && strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"") {
			val = strings.NewReplacer("\\n", "\n", "\\\"", "\"", "\\\\", "\\").Replace(val[1 : len(val)-1])
		}
		out[name] = val
	}
	return out
}
