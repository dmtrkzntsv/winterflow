package authserver

import "testing"

func TestEnabledAndURLs(t *testing.T) {
	on := New(nil, nil, nil, testURL, nil)
	if !on.Enabled() || on.ResourceURL() != testURL+"/mcp" ||
		on.ResourceMetadataURL() != testURL+"/.well-known/oauth-protected-resource/mcp" {
		t.Fatalf("enabled server: %v %q %q", on.Enabled(), on.ResourceURL(), on.ResourceMetadataURL())
	}
	if want := `Bearer resource_metadata="` + testURL + `/.well-known/oauth-protected-resource/mcp", scope="mcp"`; on.Challenge() != want {
		t.Fatalf("challenge = %q", on.Challenge())
	}
	off := New(nil, nil, nil, "", nil)
	if off.Enabled() || off.ResourceMetadataURL() != "" || off.Challenge() != "" {
		t.Fatal("a server without an issuer must be disabled")
	}
	var none *UseCase
	if none.Enabled() {
		t.Fatal("a nil server is disabled")
	}
}
