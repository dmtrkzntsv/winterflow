package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseDiff(t *testing.T) {
	in := strings.Join([]string{
		"diff --git a/internal/a.go b/internal/a.go",
		"index 1111111..2222222 100644",
		"--- a/internal/a.go",
		"+++ b/internal/a.go",
		"@@ -3,0 +4,2 @@ func A() {",
		"+\tx := 1",
		"+\ty := 2",
		"@@ -10 +12 @@ func B() {",
		"-\told()",
		"+\tnew()",
		"@@ -20,3 +21,0 @@",
		"-\tgone()",
		"-\tgone()",
		"-\tgone()",
		"diff --git a/web/src/b.tsx b/web/src/b.tsx",
		"new file mode 100644",
		"--- /dev/null",
		"+++ b/web/src/b.tsx",
		"@@ -0,0 +1,3 @@",
		"+++ this added line starts with two pluses",
		"+line two",
		"+line three",
		"diff --git a/old.go b/old.go",
		"deleted file mode 100644",
		"--- a/old.go",
		"+++ /dev/null",
		"@@ -1,2 +0,0 @@",
		"-package old",
		"-",
		"diff --git a/img.png b/img.png",
		"Binary files a/img.png and b/img.png differ",
	}, "\n")

	got, err := ParseDiff(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]int{
		"internal/a.go": {4, 5, 12},
		"web/src/b.tsx": {1, 2, 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseDiffPathQuoting(t *testing.T) {
	in := strings.Join([]string{
		"diff --git a/dir/with space.go b/dir/with space.go",
		"--- a/dir/with space.go\t",
		"+++ b/dir/with space.go\t",
		"@@ -1 +1 @@",
		"+package dir",
		`diff --git "a/q\"uote.go" "b/q\"uote.go"`,
		`--- "a/q\"uote.go"`,
		`+++ "b/q\"uote.go"`,
		"@@ -0,0 +7 @@",
		"+x",
	}, "\n")

	got, err := ParseDiff(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]int{
		"dir/with space.go": {1},
		`q"uote.go`:         {7},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseDiffBadHunk(t *testing.T) {
	in := "diff --git a/x.go b/x.go\n+++ b/x.go\n@@ garbage @@\n"
	if _, err := ParseDiff(strings.NewReader(in)); err == nil {
		t.Fatal("want error for malformed hunk header")
	}
}

func TestParseDiffEmpty(t *testing.T) {
	got, err := ParseDiff(strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}
