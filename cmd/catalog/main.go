// Command catalog builds the catalog feed from a winterflow-apps checkout
// (`catalog build`) and makes its signing key pair (`catalog keygen`); see
// internal/app/catalog.
package main

import (
	"os"

	"winterflow/internal/app/catalog"
)

func main() {
	os.Exit(catalog.Main(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}
