package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Block is one coverprofile block, reduced to the line range it spans.
type Block struct {
	StartLine, EndLine int
	Stmts, Count       int
}

// GoProfile maps a repo-relative file path to its coverage blocks.
type GoProfile map[string][]Block

// blockRE matches "path/file.go:12.34,15.2 3 1".
var blockRE = regexp.MustCompile(`^(.+\.go):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)$`)

// ParseGoProfile reads a `go test -coverprofile` file. module is the Go
// module path (from go.mod); it is stripped so paths match git's
// repo-relative ones. Duplicate blocks (same file and position, as merged
// profiles produce) keep the highest count. An empty profile (no mode line,
// or no blocks) is an error: it means the tests didn't run, and treating it
// as "nothing executable" would pass the gate silently.
func ParseGoProfile(r io.Reader, module string) (GoProfile, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	type key struct {
		file     string
		position string
	}
	seen := map[key]int{} // index into p[file]
	p := GoProfile{}
	sawMode := false
	blocks := 0
	prefix := module + "/"
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "mode: ") {
			sawMode = true
			continue
		}
		m := blockRE.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("bad coverprofile line %q", line)
		}
		file := strings.TrimPrefix(m[1], prefix)
		start, _ := strconv.Atoi(m[2])
		end, _ := strconv.Atoi(m[4])
		stmts, _ := strconv.Atoi(m[6])
		count, _ := strconv.Atoi(m[7])
		k := key{file, m[2] + "." + m[3] + "," + m[4] + "." + m[5]}
		if i, ok := seen[k]; ok {
			if count > p[file][i].Count {
				p[file][i].Count = count
			}
			continue
		}
		seen[k] = len(p[file])
		p[file] = append(p[file], Block{StartLine: start, EndLine: end, Stmts: stmts, Count: count})
		blocks++
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read coverprofile: %w", err)
	}
	if !sawMode || blocks == 0 {
		return nil, errors.New("coverprofile is empty (did the tests run?)")
	}
	return p, nil
}

// Line reports whether a line of file is executable (inside a block with at
// least one statement) and, if so, whether any such block ran.
func (p GoProfile) Line(file string, line int) (executable, covered bool) {
	for _, b := range p[file] {
		if b.Stmts == 0 || line < b.StartLine || line > b.EndLine {
			continue
		}
		executable = true
		if b.Count > 0 {
			return true, true
		}
	}
	return executable, false
}

// Has reports whether the profile has any block for file.
func (p GoProfile) Has(file string) bool { return len(p[file]) > 0 }

// Total returns covered and total statements across the profile — the same
// figure `go tool cover -func` prints as "total:".
func (p GoProfile) Total() (covered, total int) {
	for _, blocks := range p {
		for _, b := range blocks {
			total += b.Stmts
			if b.Count > 0 {
				covered += b.Stmts
			}
		}
	}
	return covered, total
}

// modulePath reads the module path from a go.mod file.
func modulePath(goMod string) (string, error) {
	f, err := os.Open(goMod)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s: no module line", goMod)
}
