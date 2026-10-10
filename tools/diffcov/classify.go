package main

import (
	"path"
	"strings"
)

// Language names used in reports and the baseline file.
const (
	LangGo  = "go"
	LangWeb = "web"
)

// Language returns which coverage report owns a repo-relative path: "go"
// for .go files, "web" for TypeScript/JavaScript sources under web/src/
// (what Vitest's coverage includes; .d.ts files hold no code), "" for
// anything else (docs, config, assets) or an excluded path.
func Language(p string) string {
	if Excluded(p) {
		return ""
	}
	switch ext := path.Ext(p); {
	case ext == ".go":
		return LangGo
	case strings.HasPrefix(p, "web/src/") && !strings.HasSuffix(p, ".d.ts") &&
		(ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx"):
		return LangWeb
	}
	return ""
}

// Excluded reports whether a path is exempt from the diff-coverage gate:
// tests, generated protobuf code, vendored shadcn/ui, binary entry points,
// the coverage tooling itself, and the SPA bootstrap.
func Excluded(p string) bool {
	switch {
	case strings.HasSuffix(p, "_test.go"),
		strings.HasSuffix(p, ".test.ts"),
		strings.HasSuffix(p, ".test.tsx"),
		strings.HasSuffix(p, ".pb.go"),
		strings.HasPrefix(p, "web/src/components/ui/"),
		strings.HasPrefix(p, "tools/"),
		p == "web/src/main.tsx",
		strings.HasPrefix(p, "cmd/") && path.Base(p) == "main.go":
		return true
	}
	return false
}
