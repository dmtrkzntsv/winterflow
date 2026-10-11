// Package cli is the hidden `backup` subcommand of the standalone and agent
// binaries. The agent runs it inside a short-lived helper container
// (alpine, --network none, the app's mounts under /src/<n>, the binary
// itself mounted read-only at /wf): it can reach the data in Docker volumes
// that the agent's own user cannot open. A dump writes the copy to stdout
// and, as stderr's last line, a JSON Result.
//
//	backup dump sqlite <path> [--entry <name>] [--since <fingerprint>]
//	backup dump files (--src <source> --entry <entry name>)... [--exclude <pattern>...] [--since <fingerprint>]
//	backup restore sqlite <path> [--entry <name>]    (the copy on stdin)
//
// Exit codes: 0 done, 3 unchanged since --since (nothing written), 4 nothing
// to back up (the database file or every path is missing), 2 bad usage,
// 1 any other failure (a restore then changed nothing).
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"winterflow/internal/infra/backup/helper"
)

// Exit codes (helper.Exit*), re-exported for this package's readers.
const (
	ExitOK        = helper.ExitOK
	ExitFailed    = helper.ExitFailed
	ExitUsage     = helper.ExitUsage
	ExitUnchanged = helper.ExitUnchanged
	ExitMissing   = helper.ExitMissing
)

// Result is the JSON line a dump ends stderr with.
type Result = helper.Result

// errMissing is a dump with nothing to back up (exit 4).
var errMissing = errors.New("missing")

// errUnchanged is a dump whose source matches --since (exit 3).
var errUnchanged = errors.New("unchanged")

// maxWarnings caps the warnings a Result carries.
const maxWarnings = 20

// Main runs `backup <args>` with the process's stdin and returns the exit
// code.
func Main(args []string, stdout, stderr io.Writer) int {
	return Run(args, os.Stdin, stdout, stderr)
}

// Run runs `backup <args>`: a dump writes to stdout, a restore reads stdin.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		return usage(stderr)
	}
	var (
		res Result
		err error
	)
	switch args[0] + " " + args[1] {
	case "dump sqlite":
		res, err = dumpSQLiteCmd(args[2:], stdout)
	case "dump files":
		res, err = dumpFilesCmd(args[2:], stdout)
	case "restore sqlite":
		res, err = restoreSQLiteCmd(args[2:], stdin)
	default:
		return usage(stderr)
	}
	code := ExitOK
	switch {
	case errors.Is(err, errUsage):
		fmt.Fprintln(stderr, err)
		return usage(stderr)
	case errors.Is(err, errUnchanged):
		res.Unchanged, code = true, ExitUnchanged // the warnings of the check stay
	case errors.Is(err, errMissing):
		res.Error, code = strings.TrimSuffix(err.Error(), ": "+errMissing.Error()), ExitMissing
	case err != nil:
		res.Error, code = err.Error(), ExitFailed
	}
	line, _ := json.Marshal(res)
	fmt.Fprintln(stderr, string(line))
	return code
}

var errUsage = errors.New("usage")

func usage(stderr io.Writer) int {
	fmt.Fprintln(stderr, `usage:
  backup dump sqlite <path> [--entry <name>] [--since <fingerprint>]
  backup dump files (--src <source> --entry <entry name>)... [--exclude <pattern>...] [--since <fingerprint>]
  backup restore sqlite <path> [--entry <name>]`)
	return ExitUsage
}

// list is a repeatable string flag.
type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, v); return nil }

func dumpSQLiteCmd(args []string, stdout io.Writer) (Result, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return Result{}, fmt.Errorf("%w: the database path comes first", errUsage)
	}
	path := args[0]
	fs := flag.NewFlagSet("dump sqlite", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	since := fs.String("since", "", "")
	entry := fs.String("entry", "", "")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		return Result{}, fmt.Errorf("%w: dump sqlite takes <path> [--entry <name>] [--since <fingerprint>]", errUsage)
	}
	return dumpSQLite(path, strings.Trim(*entry, "/"), *since, os.TempDir(), stdout)
}

func dumpFilesCmd(args []string, stdout io.Writer) (Result, error) {
	fs := flag.NewFlagSet("dump files", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var srcs, entries, exclude list
	fs.Var(&srcs, "src", "")
	fs.Var(&entries, "entry", "")
	fs.Var(&exclude, "exclude", "")
	since := fs.String("since", "", "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 || len(srcs) == 0 || len(srcs) != len(entries) {
		return Result{}, fmt.Errorf("%w: dump files takes --src <source> --entry <entry name> pairs", errUsage)
	}
	sources := make([]source, 0, len(srcs))
	for i, src := range srcs {
		if !strings.HasPrefix(src, "/") {
			return Result{}, fmt.Errorf("%w: --src %q: use an absolute path", errUsage, src)
		}
		sources = append(sources, source{path: src, entry: strings.Trim(entries[i], "/")})
	}
	return dumpFiles(sources, exclude, *since, stdout)
}
