package main

import (
	"os"
	"regexp"
	"strings"
)

// readSource reads a changed file's new-side content (the working tree at
// HEAD; diffcov runs from the repo root). A variable so tests can fake it.
var readSource = os.ReadFile

// generatedRE is the standard marker for generated Go code
// (https://go.dev/s/generatedcode).
var generatedRE = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

// isGenerated reports whether src carries the generated-code marker on a
// line before the package clause.
func isGenerated(src []byte) bool {
	for _, line := range strings.Split(string(src), "\n") {
		line = strings.TrimRight(line, "\r")
		if generatedRE.MatchString(line) {
			return true
		}
		if strings.HasPrefix(line, "package ") {
			return false
		}
	}
	return false
}

// nonCodeLines returns the 1-based numbers of lines that hold no code: blank
// lines, // comments, and lines made only of /* ... */ comment text. go cover
// reports such lines inside a statement block as part of it, so they would
// count as executable. The scan is deliberately simple (it does not parse
// string literals), which is enough for formatted Go.
func nonCodeLines(src []byte) map[int]bool {
	out := map[int]bool{}
	inBlock := false
	for i, line := range strings.Split(strings.TrimSuffix(string(src), "\n"), "\n") {
		n := i + 1
		rest := strings.TrimSpace(line)
		if inBlock {
			end := strings.Index(rest, "*/")
			if end < 0 {
				out[n] = true
				continue
			}
			inBlock = false
			rest = strings.TrimSpace(rest[end+2:])
		} else if strings.HasPrefix(rest, "/*") {
			end := strings.Index(rest[2:], "*/")
			if end < 0 {
				inBlock = true
				out[n] = true
				continue
			}
			rest = strings.TrimSpace(rest[2+end+2:])
		}
		if rest == "" || strings.HasPrefix(rest, "//") {
			out[n] = true
		}
	}
	return out
}
