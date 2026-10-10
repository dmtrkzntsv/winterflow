// Command openapi prints the OpenAPI 3.1 document generated from the route
// tables. `make openapi` redirects it into api/openapi.yaml.
package main

import (
	"fmt"
	"os"

	"winterflow/internal/app/web"
	"winterflow/internal/app/web/api/openapi"
)

func main() {
	out, err := openapi.YAML(web.APIRoutes())
	if err != nil {
		fmt.Fprintln(os.Stderr, "openapi:", err)
		os.Exit(1)
	}
	os.Stdout.Write(out)
}
