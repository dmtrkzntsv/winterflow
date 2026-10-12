package vars

import (
	"reflect"
	"sort"
	"testing"
)

func refs(s string) []string {
	m := map[string]bool{}
	References([]byte(s), m)
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
	if got := refs(src); !reflect.DeepEqual(got, want) {
		t.Fatalf("refs = %v\nwant  %v", got, want)
	}
}

func TestReferencesInEnvValues(t *testing.T) {
	if got := refs("URL=https://${BASE_DOMAIN}/x\nPORT=8080\n# ${COMMENTED}\n"); !reflect.DeepEqual(got, []string{"BASE_DOMAIN", "COMMENTED"}) {
		t.Fatalf("refs = %v", got)
	}
}
