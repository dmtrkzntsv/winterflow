package catalog

import (
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	feed "winterflow/internal/domain/catalog"
)

const usage = `usage:
  catalog build --apps DIR --out DIR --base-url URL [--sign-key-env NAME] [--source-commit SHA]
  catalog keygen
`

// Main runs cmd/catalog with args (the program name left out) and returns
// its exit code: 0 done, 1 failed, 2 a usage error.
func Main(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "build":
		return build(args[1:], stdout, stderr, getenv)
	case "keygen":
		pub, seed, err := feed.GenerateKey(rand.Reader)
		if err != nil {
			fmt.Fprintln(stderr, "catalog keygen:", err)
			return 1
		}
		fmt.Fprintf(stdout, "CATALOG_PUBLIC_KEY=%s\nCATALOG_SIGNING_KEY=%s\n", pub, seed)
		return 0
	}
	fmt.Fprint(stderr, usage)
	return 2
}

func build(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("catalog build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o Options
	var keyEnv string
	fs.StringVar(&o.Apps, "apps", "", "the winterflow-apps checkout (apps/ and assets/)")
	fs.StringVar(&o.Out, "out", "", "the output folder")
	fs.StringVar(&o.BaseURL, "base-url", "", "where the output folder is published (https)")
	fs.StringVar(&keyEnv, "sign-key-env", "", "the environment variable holding the signing key (base64 seed)")
	fs.StringVar(&o.SourceCommit, "source-commit", "", "the apps checkout's commit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if o.Apps == "" || o.Out == "" || o.BaseURL == "" || fs.NArg() > 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	if keyEnv != "" {
		v := strings.TrimSpace(getenv(keyEnv))
		if v == "" {
			fmt.Fprintf(stderr, "catalog build: %s is not set\n", keyEnv)
			return 1
		}
		key, err := feed.ParsePrivateKey(v)
		if err != nil {
			fmt.Fprintf(stderr, "catalog build: %s: %v\n", keyEnv, err)
			return 1
		}
		o.SigningKey = key
	}
	rep, err := Build(o)
	for _, s := range rep.Skipped {
		fmt.Fprintf(stdout, "skipped %s: %s\n", s.Slug, s.Reason)
	}
	for _, w := range rep.Warnings {
		fmt.Fprintf(stdout, "warning: %s\n", w)
	}
	if err != nil {
		var be *BuildError
		if !errors.As(err, &be) {
			err = fmt.Errorf("catalog build: %w", err)
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	signed := "unsigned"
	if o.SigningKey != nil {
		signed = "signed"
	}
	fmt.Fprintf(stdout, "wrote %s: %d apps, %s\n", FeedFile, len(rep.Apps), signed)
	return 0
}
