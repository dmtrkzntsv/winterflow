package contract_test

import (
	"fmt"
	"strings"
)

// lineDiff lists the lines that differ between want and got (enough to
// spot a parity break without a diff dependency).
func lineDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			fmt.Fprintf(&b, "line %d:\n  want %s\n  got  %s\n", i+1, wl, gl)
		}
	}
	return b.String()
}
