package template

import (
	"strings"
	"testing"
)

func TestValidateRefusesNonCanonicalPorts(t *testing.T) {
	for _, port := range []Port{"+80", "080", "0", "65536", " 80", "80 ", "-1", "8e1", "8080.0", ""} {
		b := tenant()
		b.Ingress[0].Port = port
		wantInvalid(t, Validate(b), "ingress[0].port")
	}
	for _, port := range []Port{"1", "80", "65535"} {
		b := tenant()
		b.Ingress[0].Port = port
		if err := Validate(b); err != nil {
			t.Errorf("port %q: %v", port, err)
		}
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	b := tenant()
	b.Ingress = append(b.Ingress, IngressRow{Host: "static.twillingate.app", Port: "8080"})
	raw, err := Encode(b)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"port":8080`) || !strings.Contains(string(raw), `"port":"${PORT}"`) {
		t.Fatalf("encoded = %s", raw)
	}
	back, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if back.Ingress[0].Port != "${PORT}" || back.Ingress[1].Port != "8080" {
		t.Fatalf("ports = %+v", back.Ingress)
	}
}

func TestPortMarshalsOnlyCanonicalIntegersAsNumbers(t *testing.T) {
	for port, want := range map[Port]string{"80": "80", "+80": `"+80"`, "080": `"080"`, "0": "0", "${PORT}": `"${PORT}"`, "99999999999999999999": `"99999999999999999999"`} {
		raw, err := port.MarshalJSON()
		if err != nil || string(raw) != want {
			t.Errorf("%q -> %s, %v; want %s", port, raw, err, want)
		}
	}
}

// renderWith renders the tenant ingress with NAME set to name (the name
// pattern removed, so the value reaches the host).
func renderWith(t *testing.T, name string) error {
	t.Helper()
	b := tenant()
	b.Parameters[0].Pattern = ""
	v, err := Resolve(b, Stored{}, given(map[string]string{"NAME": name, "VERSION": "v1", "PORT": "9001"}, map[string]string{"SESSION_KEY": "CT"}))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = Render(b, Input{AppID: "a", Values: v})
	return err
}

func TestRenderRefusesAMalformedHost(t *testing.T) {
	bad := map[string]string{
		"crlf":       "a.example.com\r\nX: y",
		"space":      "a b",
		"braces":     "a{env.SECRET}",
		"uppercase":  "Acme",
		"dot":        "acme.",
		"long":       strings.Repeat("a", 64),
		"other host": "evil.com/",
	}
	for label, v := range bad {
		t.Run(label, func(t *testing.T) {
			wantInvalid(t, renderWith(t, v), "parameters.NAME")
		})
	}
	if err := renderWith(t, "acme"); err != nil {
		t.Fatal(err)
	}
}

func TestRenderRefusesADuplicateHost(t *testing.T) {
	b := tenant()
	b.Ingress = append(b.Ingress, b.Ingress[0])
	wantInvalid(t, Validate(b), "ingress")
}

func TestValidateRefusesAHostThatCannotBeValid(t *testing.T) {
	b := tenant()
	b.Parameters[0].Pattern = ""
	b.Parameters[0].Default = strp("UPPER")
	wantInvalid(t, Validate(b), "parameters.NAME")
}

func TestRenderBoundsTheAppName(t *testing.T) {
	b := tenant()
	b.Parameters[0].Pattern = ""
	b.Ingress = nil
	for _, name := range []string{strings.Repeat("a", 256), " "} {
		v, err := Resolve(b, Stored{}, given(map[string]string{"NAME": name, "VERSION": "v1", "PORT": "9001"}, map[string]string{"SESSION_KEY": "CT"}))
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = Render(b, Input{AppID: "a", Values: v})
		wantInvalid(t, err, "parameters.NAME")
	}
	v, _ := Resolve(b, Stored{}, given(map[string]string{"NAME": strings.Repeat("a", 255), "VERSION": "v1", "PORT": "9001"}, map[string]string{"SESSION_KEY": "CT"}))
	if _, _, err := Render(b, Input{AppID: "a", Values: v}); err != nil {
		t.Fatal(err)
	}
}

func TestWarningsAcceptsNilShared(t *testing.T) {
	if got := Warnings(tenant(), nil); len(got) != 1 {
		t.Fatalf("warnings = %v", got)
	}
}
