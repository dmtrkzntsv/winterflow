package main

import (
	"fmt"
	"io"
	"sort"
)

// Coverage answers, for one language's report, whether a changed line is
// executable and covered, and whether the report knows the file at all.
type Coverage interface {
	Line(file string, line int) (executable, covered bool)
	Has(file string) bool
}

// packageAware is implemented by reports that can tell whether a file's
// package is covered even when the file itself has no data. Go needs it: a
// file with no statements (types, consts, interfaces) yields no blocks.
type packageAware interface {
	HasPackage(file string) bool
}

// Result is one language's diff-coverage outcome.
type Result struct {
	Lang      string
	Covered   int      // executable changed lines that ran
	Total     int      // executable changed lines
	WithData  int      // changed files the report knows (Go: or whose package it knows)
	Uncovered []string // "file:line", sorted
	NoData    []string // changed files the report doesn't know, sorted
}

// Percent is Covered/Total in percent; 100 when nothing is executable.
func (r Result) Percent() float64 {
	if r.Total == 0 {
		return 100
	}
	return float64(r.Covered) * 100 / float64(r.Total)
}

// Pass reports whether the result meets min percent. A language with no
// executable changed lines passes.
func (r Result) Pass(min float64) bool {
	return r.Total == 0 || float64(r.Covered)*100 >= min*float64(r.Total)
}

// Evaluate classifies every changed line by language and checks it against
// that language's report. A nil report skips the language (Total stays 0).
func Evaluate(changed map[string][]int, reports map[string]Coverage) []Result {
	results := map[string]*Result{LangGo: {Lang: LangGo}, LangWeb: {Lang: LangWeb}}
	files := make([]string, 0, len(changed))
	for f := range changed {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		lang := Language(f)
		rep := reports[lang]
		if lang == "" || rep == nil {
			continue
		}
		res := results[lang]
		if !rep.Has(f) {
			res.NoData = append(res.NoData, f)
			// Listed as no-data, but a file whose package the report knows is
			// merely statement-free, not evidence of a path mismatch.
			if pa, ok := rep.(packageAware); ok && pa.HasPackage(f) {
				res.WithData++
			}
			continue
		}
		res.WithData++
		lines := append([]int(nil), changed[f]...)
		sort.Ints(lines)
		for _, n := range lines {
			exec, cov := rep.Line(f, n)
			if !exec {
				continue
			}
			res.Total++
			if cov {
				res.Covered++
			} else {
				res.Uncovered = append(res.Uncovered, fmt.Sprintf("%s:%d", f, n))
			}
		}
	}
	return []Result{*results[LangGo], *results[LangWeb]}
}

// NoDataLangs lists the languages that have changed files in scope but where
// the report knows none of them. That means the report is unusable for the
// diff (wrong --lcov-root, absolute paths, a different checkout), not that
// the change is untested, so the gate must not pass it.
func NoDataLangs(results []Result) []string {
	var langs []string
	for _, r := range results {
		if r.WithData == 0 && len(r.NoData) > 0 {
			langs = append(langs, r.Lang)
		}
	}
	return langs
}

// Report prints the gate summary and returns true when every language
// passes. With github set it also emits workflow annotations so uncovered
// lines show up inline on the PR diff.
func Report(w io.Writer, results []Result, min float64, github bool) bool {
	ok := true
	fmt.Fprintf(w, "diff coverage (min %.1f%%)\n", min)
	for _, r := range results {
		status := "PASS"
		if !r.Pass(min) {
			status, ok = "FAIL", false
		}
		if r.Total == 0 {
			fmt.Fprintf(w, "  %-3s  no executable changed lines  %s\n", r.Lang, status)
			continue
		}
		fmt.Fprintf(w, "  %-3s  %d/%d changed lines covered (%.1f%%)  %s\n",
			r.Lang, r.Covered, r.Total, r.Percent(), status)
	}
	for _, r := range results {
		if len(r.Uncovered) > 0 {
			fmt.Fprintf(w, "uncovered changed %s lines:\n", r.Lang)
			for _, u := range r.Uncovered {
				fmt.Fprintf(w, "  %s\n", u)
			}
		}
		if len(r.NoData) > 0 {
			fmt.Fprintf(w, "changed %s files with no coverage data (types only, excluded from coverage, or never loaded by a test):\n", r.Lang)
			for _, f := range r.NoData {
				fmt.Fprintf(w, "  %s\n", f)
			}
		}
	}
	if github {
		for _, r := range results {
			for _, u := range r.Uncovered {
				file, line := splitFileLine(u)
				fmt.Fprintf(w, "::warning file=%s,line=%s::changed line not covered by tests\n", file, line)
			}
		}
	}
	return ok
}

// splitFileLine splits "path:line" at the last colon.
func splitFileLine(s string) (file, line string) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}
