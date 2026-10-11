package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoadBaseline(t *testing.T) {
	got, err := LoadBaseline(strings.NewReader(`{ "go": 59.4, "web": 0.0 }`))
	if err != nil {
		t.Fatal(err)
	}
	if got[LangGo] != 59.4 || got[LangWeb] != 0 {
		t.Fatalf("got %v", got)
	}
	for name, in := range map[string]string{
		"missing web": `{"go": 59.4}`,
		"null go":     `{"go": null, "web": 1}`,
		"typo":        `{"golang": 59.4, "web": 1}`,
		"not json":    `go=59.4`,
	} {
		if _, err := LoadBaseline(strings.NewReader(in)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestCheckRatchet(t *testing.T) {
	cases := []struct {
		total, base float64
		ok, raise   bool
	}{
		{59.4, 59.4, true, false},
		{59.3, 59.4, true, false},  // 0.1 below: tolerated
		{59.25, 59.4, true, false}, // rounds to 59.3
		{59.2, 59.4, false, false}, // more than 0.1 below
		{60.3, 59.4, true, false},  // +0.9: no bump yet
		{60.4, 59.4, true, true},   // +1.0: bump
		{5.43, 0.0, true, true},
		{0, 0, true, false},
	}
	for _, c := range cases {
		ok, raise := CheckRatchet(c.total, c.base)
		if ok != c.ok || raise != c.raise {
			t.Errorf("CheckRatchet(%v, %v) = (%v,%v), want (%v,%v)", c.total, c.base, ok, raise, c.ok, c.raise)
		}
	}
}

func TestPercent(t *testing.T) {
	if Percent(0, 0) != 0 || Percent(1, 4) != 25 {
		t.Fatal("Percent wrong")
	}
}

func TestRunRatchet(t *testing.T) {
	var buf bytes.Buffer
	ok := RunRatchet(&buf,
		map[string]float64{LangGo: 58.0, LangWeb: 6.04},
		map[string]float64{LangGo: 59.4, LangWeb: 0.0})
	if ok {
		t.Fatal("want failure: go regressed")
	}
	out := buf.String()
	for _, want := range []string{
		"go    58.0% (baseline 59.4)  FAIL",
		"web    6.0% (baseline 0.0)  PASS",
		"::error::go coverage 58.0% is below the baseline 59.4%",
		"::notice::raise coverage-baseline.json web to 6.0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}
