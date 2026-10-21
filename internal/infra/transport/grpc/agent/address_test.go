package agent

import "testing"

func TestHubAddress(t *testing.T) {
	for _, c := range []struct{ host, port, want string }{
		{"hub.example.com", "50051", "hub.example.com:50051"},
		{"192.0.2.7", "50051", "192.0.2.7:50051"},
		{"2001:db8::1", "50051", "[2001:db8::1]:50051"},
		{"[2001:db8::1]", "50051", "[2001:db8::1]:50051"},
	} {
		if got := hubAddress(c.host, c.port); got != c.want {
			t.Errorf("hubAddress(%q, %q) = %q, want %q", c.host, c.port, got, c.want)
		}
	}
}
