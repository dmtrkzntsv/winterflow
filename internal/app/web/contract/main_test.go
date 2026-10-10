package contract_test

import (
	"flag"
	"fmt"
	"os"
	"testing"
)

// TestMain sets the process-wide environment the server reads (every test
// shares it, so tests can run in parallel), then runs the suite.
func TestMain(m *testing.M) {
	flag.Parse()
	tmp, err := os.MkdirTemp("", "wf-contract-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for k, v := range map[string]string{
		"JWT_SECRET":           "contract-secret",
		"WEB_URL":              "http://127.0.0.1",
		"REGION":               "contract",
		"AVATARS_STORAGE_PATH": tmp,
		"AGENT_DATA_DIR":       tmp,
		"INGRESS_ENABLED":      "false",
	} {
		os.Setenv(k, v)
	}
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}
