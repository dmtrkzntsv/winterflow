package contract_test

import (
	"flag"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// unexercised lists table routes the suite cannot reach, with the reason.
// Keep it empty unless a route truly cannot be driven over HTTP.
var unexercised = map[string]string{}

// missingRoutes returns table routes no test called successfully, as
// "METHOD /path". Only answers below 400 count: the 401 sweep and the 400
// probes reach every route without exercising any of them.
func missingRoutes() []string {
	hits.Lock()
	defer hits.Unlock()
	reached := map[string]bool{}
	for h := range hits.set {
		if h.Status < 400 {
			reached[h.Method+" "+h.Path] = true
		}
	}
	var missing []string
	for _, rt := range table {
		key := rt.Method + " " + rt.Path
		if _, skip := unexercised[key]; skip || reached[key] {
			continue
		}
		missing = append(missing, key)
	}
	sort.Strings(missing)
	return missing
}

// coverageReport formats the guard's failure.
func coverageReport(missing []string) string {
	return fmt.Sprintf("contract coverage guard: %d route(s) in the table never answered below 400:\n  %s\nadd a happy-path contract test for each (or list it in unexercised with a reason)",
		len(missing), strings.Join(missing, "\n  "))
}

// guardEnv set to "require" (CI) turns a partial run into a failure, so a
// stray -run, -skip or -short cannot silently switch the guard off.
const guardEnv = "WF_CONTRACT_GUARD"

// partialRun names the flag that narrows this run, or "" for a full run.
func partialRun() string {
	for _, name := range []string{"test.run", "test.skip"} {
		if f := flag.Lookup(name); f != nil && f.Value.String() != "" {
			return "-" + strings.TrimPrefix(name, "test.")
		}
	}
	if testing.Short() {
		return "-short"
	}
	return ""
}

// guardApplies reports whether the coverage guard runs. Partial runs skip
// it, since most routes are deliberately not exercised, unless mode
// (WF_CONTRACT_GUARD) is "require", where they are an error.
func guardApplies(partial, mode string) (bool, error) {
	if partial == "" {
		return true, nil
	}
	if mode == "require" {
		return false, fmt.Errorf("contract coverage guard: %s=require but %s narrows the run, so the guard cannot run", guardEnv, partial)
	}
	return false, nil
}

func TestGuardApplies(t *testing.T) {
	t.Parallel()
	cases := []struct {
		partial, mode string
		apply, err    bool
	}{
		{"", "", true, false},
		{"", "require", true, false},
		{"-run", "", false, false},
		{"-short", "", false, false},
		{"-run", "require", false, true},
		{"-skip", "require", false, true},
		{"-short", "require", false, true},
	}
	for _, tc := range cases {
		apply, err := guardApplies(tc.partial, tc.mode)
		if apply != tc.apply || (err != nil) != tc.err {
			t.Errorf("guardApplies(%q, %q) = %v, %v; want %v, error %v", tc.partial, tc.mode, apply, err, tc.apply, tc.err)
		}
	}
}
