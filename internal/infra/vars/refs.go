package vars

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

// References adds to into every variable name data references: $NAME,
// ${NAME} and ${NAME:-x}, ${NAME-x}, ${NAME:?x}, ${NAME?x}, ${NAME:+x},
// ${NAME+x}. $$ is compose's escape for a literal $ and starts nothing.
// Names inside a default (${A:-${B}}) are found too.
func References(data []byte, into map[string]bool) {
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
				into[string(data[i+2:end])] = true
			}
			i++ // keep scanning inside the braces: a default can reference more names
		default:
			end := nameEnd(data, i+1)
			if end > i+1 {
				into[string(data[i+1:end])] = true
				i = end - 1
			}
		}
	}
}
