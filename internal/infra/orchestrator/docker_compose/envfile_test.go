package dockercompose

import (
	"bytes"
	"testing"
)

func TestMarshalEnvSortedAndQuoted(t *testing.T) {
	got := marshalEnv([]resolvedItem{
		{name: "ZULU", content: []byte("plain")},
		{name: "ALPHA", content: []byte("with space")},
		{name: "MULTI", content: []byte("line1\nline2")},
		{name: "QUOTE", content: []byte(`say "hi"`)},
		{name: "EMPTY", content: []byte("")},
	})
	want := "ALPHA=with space\n" +
		"EMPTY=\n" +
		"MULTI=\"line1\\nline2\"\n" +
		"QUOTE=\"say \\\"hi\\\"\"\n" +
		"ZULU=plain\n"
	if string(got) != want {
		t.Fatalf("marshalEnv:\n%q\nwant:\n%q", got, want)
	}
}

func TestParseEnvRoundTripAndNoise(t *testing.T) {
	raw := []byte("# comment\n\nA=1\nB=\"two\\nlines\"\nC=say \"hi\"\n")
	m := parseEnv(raw)
	if m["A"] != "1" || m["B"] != "two\nlines" || m["C"] != `say "hi"` {
		t.Fatalf("parseEnv = %#v", m)
	}

	// Round-trip through marshal.
	items := []resolvedItem{
		{name: "A", content: []byte("1")},
		{name: "B", content: []byte("two\nlines")},
		{name: "C", content: []byte(`say "hi"`)},
	}
	back := parseEnv(marshalEnv(items))
	for _, it := range items {
		if back[it.name] != string(it.content) {
			t.Fatalf("round-trip %s: %q != %q", it.name, back[it.name], it.content)
		}
	}
}

func TestMarshalEnvDeterministic(t *testing.T) {
	a := marshalEnv([]resolvedItem{{name: "B", content: []byte("2")}, {name: "A", content: []byte("1")}})
	b := marshalEnv([]resolvedItem{{name: "A", content: []byte("1")}, {name: "B", content: []byte("2")}})
	if !bytes.Equal(a, b) {
		t.Fatalf("order-dependent output:\n%q\n%q", a, b)
	}
}
