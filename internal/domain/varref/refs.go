// Package varref scans text for the variables compose would interpolate:
// the shared variables renderer (agent) and template validation (API)
// read references the same way.
package varref

func isNameStart(c byte) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

func isNameChar(c byte) bool { return isNameStart(c) || (c >= '0' && c <= '9') }

// nameEnd returns the end of the identifier starting at i, or i when there
// is none.
func nameEnd(data []byte, i int) int {
	if i >= len(data) || !isNameStart(data[i]) {
		return i
	}
	j := i + 1
	for j < len(data) && isNameChar(data[j]) {
		j++
	}
	return j
}

// braceForm reports whether data[k:] continues a ${NAME form compose
// interpolates: '}' or one of :- - :? ? :+ +.
func braceForm(data []byte, k int) bool {
	if k >= len(data) {
		return false
	}
	switch data[k] {
	case '}', '-', '?', '+':
		return true
	case ':':
		return k+1 < len(data) && (data[k+1] == '-' || data[k+1] == '?' || data[k+1] == '+')
	}
	return false
}

// braceDefault reports whether the ${NAME form at data[k:] (braceForm is
// true) needs no value for NAME: :- - :+ + substitute without it.
func braceDefault(data []byte, k int) bool {
	switch data[k] {
	case '-', '+':
		return true
	case ':':
		return data[k+1] == '-' || data[k+1] == '+'
	}
	return false
}

// scan calls found for every reference in data with whether that form
// gives the name a fallback. $$ is compose's escape for a literal $ and
// starts nothing. Names inside a default (${A:-${B}}) are found too.
func scan(data []byte, found func(name string, defaulted bool)) {
	for i := 0; i+1 < len(data); i++ {
		if data[i] != '$' {
			continue
		}
		switch next := data[i+1]; {
		case next == '$':
			i++ // literal $
		case next == '{':
			end := nameEnd(data, i+2)
			if end > i+2 && braceForm(data, end) {
				found(string(data[i+2:end]), braceDefault(data, end))
			}
			i++ // keep scanning inside the braces: a default can reference more names
		default:
			end := nameEnd(data, i+1)
			if end > i+1 {
				found(string(data[i+1:end]), false)
				i = end - 1
			}
		}
	}
}

// References adds to into every variable name data references: $NAME,
// ${NAME} and ${NAME:-x}, ${NAME-x}, ${NAME:?x}, ${NAME?x}, ${NAME:+x},
// ${NAME+x}.
func References(data []byte, into map[string]bool) {
	scan(data, func(name string, _ bool) { into[name] = true })
}

// Undefaulted adds to into every name data references at least once in a
// form that needs a value: $NAME, ${NAME}, ${NAME:?x}, ${NAME?x}. A name
// only ever referenced as ${NAME:-x}, ${NAME-x}, ${NAME:+x} or ${NAME+x}
// is left out.
func Undefaulted(data []byte, into map[string]bool) {
	scan(data, func(name string, defaulted bool) {
		if !defaulted {
			into[name] = true
		}
	})
}
