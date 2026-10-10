package command

import "testing"

func TestValidAppID(t *testing.T) {
	for id, want := range map[string]bool{
		"app-1": true, "a": true, "..foo": true, "0194a7c2-7b1e-7000-8000-000000000000": true,
		"": false, ".": false, "..": false, "/": false, "a/..": false, "../..": false,
		"x/../..": false, "a/b": false, "/abs": false, `a\b`: false, "a\x00b": false,
	} {
		if got := ValidAppID(id); got != want {
			t.Errorf("ValidAppID(%q) = %v, want %v", id, got, want)
		}
	}
}
