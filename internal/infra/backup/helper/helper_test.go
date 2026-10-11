package helper

import "testing"

func TestParseResult(t *testing.T) {
	res, ok := ParseResult([]byte("some log\n{\"fingerprint\":\"files:ab\",\"raw_size\":12,\"warnings\":[\"w\"]}\n\n"))
	if !ok || res.Fingerprint != "files:ab" || res.RawSize != 12 || len(res.Warnings) != 1 {
		t.Fatalf("result = %+v %v", res, ok)
	}
	for _, s := range []string{"", "docker: Error response from daemon: no such image", "{broken"} {
		if _, ok := ParseResult([]byte(s)); ok {
			t.Errorf("%q parsed", s)
		}
	}
}
