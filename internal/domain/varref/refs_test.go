package varref

import (
	"reflect"
	"sort"
	"testing"
)

func names(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestUndefaultedSkipsNamesWithAFallback(t *testing.T) {
	src := `A: $PLAIN
B: ${BRACED}
C: ${COLON_DASH:-x}
D: ${DASH-x}
E: ${COLON_Q:?required}
F: ${Q?required}
G: ${COLON_PLUS:+set}
H: ${PLUS+set}
I: $$ESCAPED
K: ${OUTER:-${INNER}}
L: ${BOTH:-x} and ${BOTH}
`
	got := map[string]bool{}
	Undefaulted([]byte(src), got)
	want := []string{"BOTH", "BRACED", "COLON_Q", "INNER", "PLAIN", "Q"}
	if !reflect.DeepEqual(names(got), want) {
		t.Fatalf("undefaulted = %v\nwant          %v", names(got), want)
	}
	all := map[string]bool{}
	References([]byte(src), all)
	if len(all) != 11 { // every name but the escaped one
		t.Fatalf("references = %v", names(all))
	}
}
