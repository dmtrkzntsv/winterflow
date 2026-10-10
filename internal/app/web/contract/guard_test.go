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

// missingRoutes returns table routes no test reached, as "METHOD /path".
func missingRoutes() []string {
	hits.Lock()
	defer hits.Unlock()
	reached := map[string]bool{}
	for h := range hits.set {
		reached[h.Method+" "+h.Path] = true
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
	return fmt.Sprintf("contract coverage guard: %d route(s) in the table were never called:\n  %s\nadd a contract test for each (or list it in unexercised with a reason)",
		len(missing), strings.Join(missing, "\n  "))
}

// guardApplies is false for partial runs (-run, -skip) and -short, where
// most routes are deliberately not exercised.
func guardApplies() bool {
	for _, name := range []string{"test.run", "test.skip"} {
		if f := flag.Lookup(name); f != nil && f.Value.String() != "" {
			return false
		}
	}
	return !testing.Short()
}
