package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
)

// Lcov maps a repo-relative file path to line → hit count, from DA records.
type Lcov map[string]map[int]int

// ParseLcov reads an lcov tracefile. Vitest writes SF paths relative to the
// web project root, so root ("web") is joined in front of relative paths to
// make them repo-relative. Repeated records for a file or line keep the
// highest hit count. A tracefile with no SF record is an error (the coverage
// run didn't happen or wrote nothing).
func ParseLcov(r io.Reader, root string) (Lcov, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	out := Lcov{}
	var file string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "SF:"):
			sf := strings.TrimPrefix(line, "SF:")
			if !path.IsAbs(sf) {
				sf = path.Join(root, sf)
			}
			file = sf
			if out[file] == nil {
				out[file] = map[int]int{}
			}
		case strings.HasPrefix(line, "DA:"):
			if file == "" {
				return nil, fmt.Errorf("lcov DA record outside a file: %q", line)
			}
			parts := strings.Split(strings.TrimPrefix(line, "DA:"), ",")
			if len(parts) < 2 {
				return nil, fmt.Errorf("bad lcov DA record %q", line)
			}
			n, err1 := strconv.Atoi(parts[0])
			hits, err2 := strconv.Atoi(parts[1])
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("bad lcov DA record %q", line)
			}
			if prev, ok := out[file][n]; !ok || hits > prev {
				out[file][n] = hits
			}
		case line == "end_of_record":
			file = ""
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read lcov: %w", err)
	}
	if len(out) == 0 {
		return nil, errors.New("lcov has no files (did the web tests run with --coverage?)")
	}
	return out, nil
}

// Line reports whether a line has a DA record and, if so, whether it ran.
func (l Lcov) Line(file string, line int) (executable, covered bool) {
	hits, ok := l[file][line]
	return ok, ok && hits > 0
}

// Has reports whether the tracefile has a record for file.
func (l Lcov) Has(file string) bool { _, ok := l[file]; return ok }

// Total returns covered and total lines across every DA record.
func (l Lcov) Total() (covered, total int) {
	for _, lines := range l {
		for _, hits := range lines {
			total++
			if hits > 0 {
				covered++
			}
		}
	}
	return covered, total
}
