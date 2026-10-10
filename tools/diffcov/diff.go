package main

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// hunkRE matches a unified-diff hunk header and captures the new-side start
// line and (optional) line count: "@@ -10,2 +12,3 @@".
var hunkRE = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// ParseDiff reads `git diff --unified=0` output and returns the added line
// numbers per repo-relative file path (new side). Deleted files and pure
// deletions contribute nothing. "+++ " is only treated as a file header
// between "diff --git" and the first hunk, so an added line whose content
// starts with "++ " is still counted as an added line.
func ParseDiff(r io.Reader) (map[string][]int, error) {
	out := map[string][]int{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var file string
	inHeader := false
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "diff --git "):
			inHeader, file = true, ""
		case inHeader && strings.HasPrefix(line, "+++ "):
			name, err := diffPath(strings.TrimPrefix(line, "+++ "))
			if err != nil {
				return nil, err
			}
			file = name
		case strings.HasPrefix(line, "@@ "):
			inHeader = false
			if file == "" {
				continue
			}
			m := hunkRE.FindStringSubmatch(line)
			if m == nil {
				return nil, fmt.Errorf("bad hunk header in %s: %q", file, line)
			}
			start, _ := strconv.Atoi(m[1])
			count := 1
			if m[2] != "" {
				count, _ = strconv.Atoi(m[2])
			}
			for i := 0; i < count; i++ {
				out[file] = append(out[file], start+i)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read diff: %w", err)
	}
	return out, nil
}

// diffPath turns the target of a "+++ " header into a repo-relative path.
// It returns "" for /dev/null (a deleted file). Git appends a tab to names
// containing spaces and C-quotes names with special characters.
func diffPath(s string) (string, error) {
	s = strings.TrimSuffix(s, "\t")
	if s == "/dev/null" {
		return "", nil
	}
	if strings.HasPrefix(s, `"`) {
		u, err := strconv.Unquote(s)
		if err != nil {
			return "", fmt.Errorf("unquote diff path %s: %w", s, err)
		}
		s = u
	}
	return strings.TrimPrefix(s, "b/"), nil
}
