package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func testReports(t *testing.T) map[string]Coverage {
	t.Helper()
	gp, err := ParseGoProfile(strings.NewReader(`mode: atomic
winterflow/internal/a.go:10.1,12.2 2 1
winterflow/internal/a.go:13.1,14.2 1 0
`), "winterflow")
	if err != nil {
		t.Fatal(err)
	}
	lc, err := ParseLcov(strings.NewReader(`SF:src/x.tsx
DA:1,1
DA:2,0
end_of_record
`), "web")
	if err != nil {
		t.Fatal(err)
	}
	return map[string]Coverage{LangGo: gp, LangWeb: lc}
}

func TestEvaluate(t *testing.T) {
	changed := map[string][]int{
		"internal/a.go":      {14, 10, 11, 20}, // 20 is outside every block
		"internal/new.go":    {1, 2},           // not in the profile at all
		"internal/a_test.go": {1},              // excluded
		"web/src/x.tsx":      {1, 2, 3},        // 3 has no DA record
		"docs/readme.md":     {1},              // no language
	}
	got := Evaluate(changed, testReports(t))
	want := []Result{
		{Lang: LangGo, Covered: 2, Total: 3, WithData: 1, Uncovered: []string{"internal/a.go:14"}, NoData: []string{"internal/new.go"}},
		{Lang: LangWeb, Covered: 1, Total: 2, WithData: 1, Uncovered: []string{"web/src/x.tsx:2"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestEvaluateSkipsMissingReport(t *testing.T) {
	reports := testReports(t)
	delete(reports, LangWeb)
	got := Evaluate(map[string][]int{"web/src/x.tsx": {2}}, reports)
	if got[1].Total != 0 || len(got[1].NoData) != 0 {
		t.Fatalf("web without a report should be skipped, got %+v", got[1])
	}
}

func TestResultPass(t *testing.T) {
	cases := []struct {
		covered, total int
		want           bool
	}{
		{0, 0, true},  // nothing executable passes
		{4, 5, true},  // exactly 80%
		{3, 4, false}, // 75%
		{79, 100, false},
		{80, 100, true},
		{0, 1, false},
	}
	for _, c := range cases {
		r := Result{Covered: c.covered, Total: c.total}
		if got := r.Pass(80); got != c.want {
			t.Errorf("Pass(80) for %d/%d = %v, want %v", c.covered, c.total, got, c.want)
		}
	}
}

func TestReport(t *testing.T) {
	results := []Result{
		{Lang: LangGo, Covered: 3, Total: 4, Uncovered: []string{"internal/a.go:14"}, NoData: []string{"internal/new.go"}},
		{Lang: LangWeb},
	}
	var buf bytes.Buffer
	if Report(&buf, results, 80, true) {
		t.Fatal("Report = pass, want fail (go at 75%)")
	}
	out := buf.String()
	for _, want := range []string{
		"go   3/4 changed lines covered (75.0%)  FAIL",
		"web  no executable changed lines  PASS",
		"  internal/a.go:14",
		"  internal/new.go",
		"::warning file=internal/a.go,line=14::changed line not covered by tests",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}

	buf.Reset()
	if !Report(&buf, []Result{{Lang: LangGo, Covered: 4, Total: 5}, {Lang: LangWeb}}, 80, false) {
		t.Fatalf("Report = fail, want pass:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "::warning") {
		t.Error("annotations printed without github flag")
	}
}

func TestNoDataLangs(t *testing.T) {
	results := []Result{
		{Lang: LangGo, NoData: []string{"a.go"}},                   // every changed file unknown
		{Lang: LangWeb, WithData: 1, NoData: []string{"web/x.ts"}}, // mixed: fine
	}
	got := NoDataLangs(results)
	if !reflect.DeepEqual(got, []string{LangGo}) {
		t.Fatalf("got %v, want [go]", got)
	}
	if got := NoDataLangs([]Result{{Lang: LangGo}, {Lang: LangWeb, WithData: 2}}); len(got) != 0 {
		t.Fatalf("got %v, want none", got)
	}
}
