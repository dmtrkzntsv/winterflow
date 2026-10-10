// Command diffcov is CI's coverage gate. It has two modes:
//
//	diffcov --base <ref> --go coverage.out --lcov web/coverage/lcov.info [--min 80] [--github]
//
// checks that the lines added since <ref> (git diff <ref>...HEAD) are at
// least --min percent covered, per language, and
//
//	diffcov ratchet --go coverage.out --lcov web/coverage/lcov.info [--baseline coverage-baseline.json]
//
// checks the total Go and web coverage against the committed baseline.
// Stdlib only. Exit status: 0 pass, 1 coverage below the bar, 2 usage or
// input error.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "ratchet" {
		return runRatchet(args[1:], stdout, stderr)
	}
	return runGate(args, stdout, stderr)
}

func runGate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("diffcov", flag.ContinueOnError)
	fs.SetOutput(stderr)
	base := fs.String("base", "", "base ref or SHA; lines added in <base>...HEAD are checked (required)")
	goProfile := fs.String("go", "", "Go coverprofile (go test -coverprofile); empty skips Go")
	lcovPath := fs.String("lcov", "", "web lcov tracefile; empty skips web")
	lcovRoot := fs.String("lcov-root", "web", "directory lcov SF paths are relative to")
	goMod := fs.String("gomod", "go.mod", "go.mod to read the module path from")
	min := fs.Float64("min", 80, "minimum percent of executable changed lines covered, per language")
	github := fs.Bool("github", false, "also print GitHub Actions annotations for uncovered lines")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *base == "" || (*goProfile == "" && *lcovPath == "") {
		fmt.Fprintln(stderr, "diffcov: --base and at least one of --go/--lcov are required")
		return 2
	}

	reports, err := loadReports(*goProfile, *goMod, *lcovPath, *lcovRoot)
	if err != nil {
		fmt.Fprintln(stderr, "diffcov:", err)
		return 2
	}
	diff, err := gitDiff(*base)
	if err != nil {
		fmt.Fprintln(stderr, "diffcov:", err)
		return 2
	}
	changed, err := ParseDiff(bytes.NewReader(diff))
	if err != nil {
		fmt.Fprintln(stderr, "diffcov:", err)
		return 2
	}
	results := Evaluate(changed, reports)
	ok := Report(stdout, results, *min, *github)
	if langs := NoDataLangs(results); len(langs) > 0 {
		for _, lang := range langs {
			fmt.Fprintf(stderr, "diffcov: no coverage data for any changed %s file — check report paths / --lcov-root\n", lang)
		}
		return 2
	}
	if !ok {
		return 1
	}
	return 0
}

// loadReports parses whichever reports were given; an empty path leaves
// that language out of the map.
func loadReports(goProfile, goMod, lcovPath, lcovRoot string) (map[string]Coverage, error) {
	reports := map[string]Coverage{}
	if goProfile != "" {
		p, err := loadGoProfile(goProfile, goMod)
		if err != nil {
			return nil, err
		}
		reports[LangGo] = p
	}
	if lcovPath != "" {
		l, err := loadLcov(lcovPath, lcovRoot)
		if err != nil {
			return nil, err
		}
		reports[LangWeb] = l
	}
	return reports, nil
}

func loadGoProfile(file, goMod string) (GoProfile, error) {
	module, err := modulePath(goMod)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	p, err := ParseGoProfile(f, module)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return p, nil
}

func loadLcov(file, root string) (Lcov, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	l, err := ParseLcov(f, root)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return l, nil
}

// gitDiff returns the zero-context diff of HEAD against its merge base with
// base. Renames are detected so a moved file only counts its edited lines.
func gitDiff(base string) ([]byte, error) {
	cmd := exec.Command("git", "-c", "core.quotePath=false", "diff",
		"--unified=0", "--no-color", "--no-ext-diff", "-M", base+"...HEAD")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff %s...HEAD: %v: %s (is the base fetched? CI needs fetch-depth: 0)",
			base, err, bytes.TrimSpace(stderr.Bytes()))
	}
	return out, nil
}
