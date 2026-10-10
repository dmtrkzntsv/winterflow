package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
)

// Ratchet tolerances, in tenths of a percentage point: a total may sit at
// most 0.1 below its baseline, and a gain of 1.0 or more asks for a bump.
const (
	ratchetSlack  = 1
	ratchetNotice = 10
)

// LoadBaseline reads coverage-baseline.json ({"go": 59.4, "web": 0.0}).
// Both keys are required so a typo can't silently disable a check.
func LoadBaseline(r io.Reader) (map[string]float64, error) {
	var raw map[string]*float64
	dec := json.NewDecoder(r)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse baseline: %w", err)
	}
	out := map[string]float64{}
	for _, lang := range []string{LangGo, LangWeb} {
		v, ok := raw[lang]
		if !ok || v == nil {
			return nil, fmt.Errorf("baseline has no %q value", lang)
		}
		out[lang] = *v
	}
	return out, nil
}

// Percent returns covered/total in percent, 0 for an empty total.
func Percent(covered, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(covered) * 100 / float64(total)
}

// tenths rounds a percentage to tenths of a point, the precision
// `go tool cover` prints and the baseline file stores.
func tenths(x float64) int { return int(math.Round(x * 10)) }

// CheckRatchet compares one language's total with its baseline. It fails
// when the total is more than 0.1 below the baseline, and asks for a
// baseline bump (raise=true) when it is at least 1.0 above.
func CheckRatchet(total, baseline float64) (ok, raise bool) {
	t, b := tenths(total), tenths(baseline)
	return t >= b-ratchetSlack, t >= b+ratchetNotice
}

// RunRatchet prints each language's total against its baseline, with
// GitHub annotations for failures and suggested bumps, and returns true
// when no total regressed.
func RunRatchet(w io.Writer, totals, baseline map[string]float64) bool {
	pass := true
	fmt.Fprintln(w, "coverage totals")
	for _, lang := range []string{LangGo, LangWeb} {
		total, base := totals[lang], baseline[lang]
		ok, raise := CheckRatchet(total, base)
		status := "PASS"
		if !ok {
			status, pass = "FAIL", false
		}
		fmt.Fprintf(w, "  %-3s  %5.1f%% (baseline %.1f)  %s\n", lang, total, base, status)
		switch {
		case !ok:
			fmt.Fprintf(w, "::error::%s coverage %.1f%% is below the baseline %.1f%% in coverage-baseline.json; add tests\n", lang, total, base)
		case raise:
			fmt.Fprintf(w, "::notice::raise coverage-baseline.json %s to %.1f\n", lang, float64(tenths(total))/10)
		}
	}
	return pass
}

func runRatchet(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("diffcov ratchet", flag.ContinueOnError)
	fs.SetOutput(stderr)
	goProfile := fs.String("go", "", "Go coverprofile (required)")
	lcovPath := fs.String("lcov", "", "web lcov tracefile (required)")
	lcovRoot := fs.String("lcov-root", "web", "directory lcov SF paths are relative to")
	goMod := fs.String("gomod", "go.mod", "go.mod to read the module path from")
	baselinePath := fs.String("baseline", "coverage-baseline.json", "baseline file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *goProfile == "" || *lcovPath == "" {
		fmt.Fprintln(stderr, "diffcov ratchet: --go and --lcov are required")
		return 2
	}
	gp, err := loadGoProfile(*goProfile, *goMod)
	if err != nil {
		fmt.Fprintln(stderr, "diffcov ratchet:", err)
		return 2
	}
	lc, err := loadLcov(*lcovPath, *lcovRoot)
	if err != nil {
		fmt.Fprintln(stderr, "diffcov ratchet:", err)
		return 2
	}
	f, err := os.Open(*baselinePath)
	if err != nil {
		fmt.Fprintln(stderr, "diffcov ratchet:", err)
		return 2
	}
	defer f.Close()
	baseline, err := LoadBaseline(f)
	if err != nil {
		fmt.Fprintf(stderr, "diffcov ratchet: %s: %v\n", *baselinePath, err)
		return 2
	}
	totals := map[string]float64{
		LangGo:  Percent(gp.Total()),
		LangWeb: Percent(lc.Total()),
	}
	if !RunRatchet(stdout, totals, baseline) {
		return 1
	}
	return 0
}
