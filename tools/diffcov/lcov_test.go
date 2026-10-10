package main

import (
	"strings"
	"testing"
)

const sampleLcov = `TN:
SF:src/lib/volumes.ts
FN:5,volumeNameError
DA:5,3
DA:6,0
DA:7,2,abcdef
LF:3
LH:2
end_of_record
TN:
SF:src/pages/x.tsx
DA:1,0
end_of_record
SF:src/pages/x.tsx
DA:1,1
DA:2,0
end_of_record
SF:src/types.ts
end_of_record
`

func TestParseLcov(t *testing.T) {
	l, err := ParseLcov(strings.NewReader(sampleLcov), "web")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		file          string
		line          int
		exec, covered bool
	}{
		{"web/src/lib/volumes.ts", 5, true, true},
		{"web/src/lib/volumes.ts", 6, true, false},
		{"web/src/lib/volumes.ts", 7, true, true}, // checksum field ignored
		{"web/src/lib/volumes.ts", 8, false, false},
		{"web/src/pages/x.tsx", 1, true, true}, // repeated record: max hits
		{"web/src/pages/x.tsx", 2, true, false},
		{"src/lib/volumes.ts", 5, false, false}, // only repo-relative paths
	}
	for _, c := range cases {
		exec, cov := l.Line(c.file, c.line)
		if exec != c.exec || cov != c.covered {
			t.Errorf("%s:%d = (%v,%v), want (%v,%v)", c.file, c.line, exec, cov, c.exec, c.covered)
		}
	}
	if !l.Has("web/src/types.ts") || l.Has("web/src/missing.ts") {
		t.Error("Has: wrong file membership")
	}
	cov, total := l.Total()
	if cov != 3 || total != 5 {
		t.Fatalf("Total = %d/%d, want 3/5", cov, total)
	}
}

func TestParseLcovAbsolutePathKept(t *testing.T) {
	l, err := ParseLcov(strings.NewReader("SF:/abs/a.ts\nDA:1,1\nend_of_record\n"), "web")
	if err != nil {
		t.Fatal(err)
	}
	if !l.Has("/abs/a.ts") {
		t.Fatalf("got %v", l)
	}
}

func TestParseLcovErrors(t *testing.T) {
	for name, in := range map[string]string{
		"empty":     "",
		"no files":  "TN:\n",
		"orphan DA": "DA:1,1\n",
		"bad DA":    "SF:a.ts\nDA:x,1\n",
		"short DA":  "SF:a.ts\nDA:1\n",
	} {
		if _, err := ParseLcov(strings.NewReader(in), "web"); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
