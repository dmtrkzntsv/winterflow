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

func TestExitPredicates(t *testing.T) {
	for exit, want := range map[int]bool{ExitOK: false, ExitFailed: true, ExitUsage: false, ExitUnchanged: false, ExitPartial: false, 124: false, 125: true, 126: true, 127: true, 128: false, -1: false} {
		if got := LeftDataUnchanged(exit); got != want {
			t.Errorf("LeftDataUnchanged(%d) = %v", exit, got)
		}
	}
	for exit, want := range map[int]bool{ExitOK: true, ExitFailed: false, ExitUsage: false, ExitUnchanged: true, ExitMissing: true, ExitPartial: true, 125: false, -1: false} {
		if got := ContainerGone(exit); got != want {
			t.Errorf("ContainerGone(%d) = %v", exit, got)
		}
	}
}
