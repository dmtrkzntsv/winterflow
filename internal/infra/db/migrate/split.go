package migrate

import "strings"

// Split cuts a migration into statements on top-level semicolons. Semicolons
// inside '...' strings, "..." identifiers, -- line comments and /* */ block
// comments do not split. Pieces holding nothing but comments and whitespace
// are dropped. Postgres dollar quoting ($$) is not supported; no migration
// uses it.
func Split(sqlText string) []string {
	var (
		out     []string
		b       strings.Builder
		hasCode bool
		inSQ    bool
		inDQ    bool
		inLine  bool
		inBlock bool
	)
	flush := func() {
		if s := strings.TrimSpace(b.String()); s != "" && hasCode {
			out = append(out, s)
		}
		b.Reset()
		hasCode = false
	}
	for i := 0; i < len(sqlText); i++ {
		c := sqlText[i]
		next := byte(0)
		if i+1 < len(sqlText) {
			next = sqlText[i+1]
		}
		switch {
		case inLine:
			if c == '\n' {
				inLine = false
			}
		case inBlock:
			if c == '*' && next == '/' {
				b.WriteByte(c)
				i++
				c = next
				inBlock = false
			}
		case inSQ:
			if c == '\'' {
				inSQ = false
			}
		case inDQ:
			if c == '"' {
				inDQ = false
			}
		case c == '-' && next == '-':
			inLine = true
		case c == '/' && next == '*':
			inBlock = true
		case c == '\'':
			inSQ, hasCode = true, true
		case c == '"':
			inDQ, hasCode = true, true
		case c == ';':
			flush()
			continue
		default:
			if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
				hasCode = true
			}
		}
		b.WriteByte(c)
	}
	flush()
	return out
}
