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

func TestReferencesEveryInterpolationForm(t *testing.T) {
	src := `services:
  app:
    image: "nginx:${TAG:-latest}"
    environment:
      A: $PLAIN
      B: ${BRACED}
      C: ${COLON_DASH:-x}
      D: ${DASH-x}
      E: ${COLON_Q:?required}
      F: ${Q?required}
      G: ${COLON_PLUS:+set}
      H: ${PLUS+set}
      I: $$ESCAPED
      J: $${ESCAPED_BRACED}
      K: ${OUTER:-${INNER}}
      L: $$$AFTER_ESCAPE
      M: price 5$ and $1 and ${2X} and ${UNTERMINATED
      N: ${lower_case}
`
	want := []string{"AFTER_ESCAPE", "BRACED", "COLON_DASH", "COLON_PLUS", "COLON_Q", "DASH", "INNER", "OUTER",
		"PLAIN", "PLUS", "Q", "TAG", "lower_case"}
	got := map[string]bool{}
	References([]byte(src), got)
	if !reflect.DeepEqual(names(got), want) {
		t.Fatalf("refs = %v\nwant  %v", names(got), want)
	}
}

func TestReferencesInEnvValues(t *testing.T) {
	got := map[string]bool{}
	References([]byte("URL=https://${BASE_DOMAIN}/x\nPORT=8080\n# ${COMMENTED}\n"), got)
	if !reflect.DeepEqual(names(got), []string{"BASE_DOMAIN", "COMMENTED"}) {
		t.Fatalf("refs = %v", names(got))
	}
}
