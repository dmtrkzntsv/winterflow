package catalog

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/template"
)

const budgetCompose = `services:
  app:
    image: actualbudget/actual-server:latest
    environment:
      # The public URL, e.g. https://budget.example.com
      ACTUAL_URL: "http://localhost:5006"
    volumes:
      - data:/data
volumes:
  data:
x-winterflow:
  expose:
    - { container: app, port: 5006 }
  variables:
    ACTUAL_URL: { required: true, pattern: "^https?://.+" }
`

// entry is a valid feed entry.
func entry() Entry {
	files := map[string]string{"compose.yml": budgetCompose}
	return Entry{
		Slug: "actual-budget", Name: "Actual Budget",
		Summary: "A local-first personal finance app", Description: "Envelope budgeting.",
		Keywords: []string{"budget"}, AlternativeTo: []string{"YNAB"}, License: "MIT",
		HomepageURL: "https://actualbudget.org", LogoURL: "https://winterflow.io/catalog/logos/actual-budget.png",
		Files: files, Digest: Digest(files),
	}
}

func TestDigest(t *testing.T) {
	a := Digest(map[string]string{"compose.yml": "x", "b.conf": "y"})
	if a != Digest(map[string]string{"b.conf": "y", "compose.yml": "x"}) || !strings.HasPrefix(a, "sha256:") || len(a) != len("sha256:")+64 {
		t.Fatalf("digest = %q", a)
	}
	// A name and its content never shift into each other.
	if Digest(map[string]string{"ab": "c"}) == Digest(map[string]string{"a": "bc"}) {
		t.Fatal("ab/c and a/bc share a digest")
	}
	if Digest(map[string]string{"compose.yml": "x"}) == Digest(map[string]string{"compose.yml": "x "}) {
		t.Fatal("a changed file kept the digest")
	}
	if Digest(nil) != Digest(map[string]string{}) {
		t.Fatal("no files")
	}
}

func TestSignAndVerify(t *testing.T) {
	pub, seed, err := GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := ParsePublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	sk, err := ParsePrivateKey(seed)
	if err != nil {
		t.Fatal(err)
	}
	feed := []byte(`{"format":1}`)
	sig := Sign(sk, feed)
	if !bytes.HasSuffix(sig, []byte("\n")) {
		t.Fatalf("sig = %q", sig)
	}
	if err := Verify(pk, feed, sig); err != nil {
		t.Fatal(err)
	}
	if err := Verify(pk, feed, bytes.TrimSpace(sig)); err != nil {
		t.Fatalf("without the newline: %v", err)
	}
	// The 64-byte private key reads too.
	full, err := ParsePrivateKey(base64.StdEncoding.EncodeToString(sk))
	if err != nil || !full.Equal(sk) {
		t.Fatalf("64-byte key = %v", err)
	}
	if _, _, err := GenerateKey(iotest.ErrReader(errors.New("no entropy"))); err == nil {
		t.Fatal("a key from a failing reader")
	}
	other, _, _ := GenerateKey(rand.Reader)
	otherKey, _ := ParsePublicKey(other)
	for name, c := range map[string]struct {
		key       ed25519.PublicKey
		feed, sig []byte
	}{
		"another key":       {otherKey, feed, sig},
		"a changed feed":    {pk, []byte(`{"format":1} `), sig},
		"not base64":        {pk, feed, []byte("not a signature")},
		"a short signature": {pk, feed, []byte(base64.StdEncoding.EncodeToString([]byte("short")))},
		"an empty .sig":     {pk, feed, nil},
		"a nil key":         {nil, feed, sig},
	} {
		if err := Verify(c.key, c.feed, c.sig); !errors.Is(err, ErrSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestParseKeysRefuse(t *testing.T) {
	for _, s := range []string{"", "not base64!", base64.StdEncoding.EncodeToString(make([]byte, 31))} {
		if _, err := ParsePublicKey(s); err == nil {
			t.Errorf("public key %q accepted", s)
		}
		if _, err := ParsePrivateKey(s); err == nil {
			t.Errorf("private key %q accepted", s)
		}
	}
	// A 64-byte key whose public half is not its seed's.
	bad := make([]byte, ed25519.PrivateKeySize)
	if _, err := ParsePrivateKey(base64.StdEncoding.EncodeToString(bad)); err == nil {
		t.Error("an inconsistent 64-byte key was accepted")
	}
}

func TestDecode(t *testing.T) {
	at := time.Date(2026, 10, 14, 20, 5, 43, 0, time.UTC)
	raw, err := Encode(Feed{Format: FormatVersion, SourceCommit: "49bf28d", GeneratedAt: at, Apps: []Entry{entry()}})
	if err != nil {
		t.Fatal(err)
	}
	f, err := Decode(raw)
	if err != nil || f.SourceCommit != "49bf28d" || !f.GeneratedAt.Equal(at) || len(f.Apps) != 1 || f.Apps[0].Files["compose.yml"] != budgetCompose {
		t.Fatalf("decoded %+v, %v", f, err)
	}
	// A later format's new fields are ignored.
	if _, err := Decode([]byte(`{"format":1,"generated_at":"2026-10-14T20:05:43Z","apps":[{"slug":"a","stars":5}],"signed_by":"x"}`)); err != nil {
		t.Fatalf("unknown fields: %v", err)
	}
	for name, src := range map[string]string{
		"not JSON":          `{"format":`,
		"format 2":          `{"format":2,"generated_at":"2026-10-14T20:05:43Z","apps":[{"slug":"a"}]}`,
		"no format":         `{"generated_at":"2026-10-14T20:05:43Z","apps":[{"slug":"a"}]}`,
		"no generated_at":   `{"format":1,"apps":[{"slug":"a"}]}`,
		"no apps":           `{"format":1,"generated_at":"2026-10-14T20:05:43Z","apps":[]}`,
		"a slug twice":      `{"format":1,"generated_at":"2026-10-14T20:05:43Z","apps":[{"slug":"a"},{"slug":"a"}]}`,
		"apps is no list":   `{"format":1,"generated_at":"2026-10-14T20:05:43Z","apps":{}}`,
		"generated_at text": `{"format":1,"generated_at":"yesterday","apps":[{"slug":"a"}]}`,
	} {
		if _, err := Decode([]byte(src)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCheck(t *testing.T) {
	body, meta, err := entry().Check()
	if err != nil {
		t.Fatal(err)
	}
	if body.Files["compose.yml"] != budgetCompose || body.Summary != "A local-first personal finance app" || body.Icon != "" {
		t.Fatalf("body = %+v", body)
	}
	want := model.CatalogMeta{
		Summary: "A local-first personal finance app", Description: "Envelope budgeting.",
		Keywords: []string{"budget"}, Tags: []string{}, AlternativeTo: []string{"YNAB"}, License: "MIT",
		HomepageURL: "https://actualbudget.org", LogoURL: "https://winterflow.io/catalog/logos/actual-budget.png",
		LicenseURL: "", Sponsorship: []model.CatalogLink{},
	}
	if !meta.Equal(want) || meta.Tags == nil || meta.Sponsorship == nil {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestCheckPreservesOptionalMetadata(t *testing.T) {
	e := entry()
	stars := 5
	e.Stars = &stars
	e.LicenseURL = "https://example.com/license"
	e.Sponsorship = []model.CatalogLink{{Name: "Open Collective", URL: "https://example.com/sponsor"}}
	_, meta, err := e.Check()
	if err != nil || meta.Stars == nil || *meta.Stars != stars || meta.LicenseURL != e.LicenseURL || !slices.Equal(meta.Sponsorship, e.Sponsorship) {
		t.Fatalf("meta = %+v, err = %v", meta, err)
	}
	raw, err := Encode(Feed{Format: FormatVersion, GeneratedAt: time.Now(), Apps: []Entry{e}})
	if err != nil {
		t.Fatal(err)
	}
	f, err := Decode(raw)
	if err != nil || f.Apps[0].Stars == nil || *f.Apps[0].Stars != stars || f.Apps[0].LicenseURL != e.LicenseURL || !slices.Equal(f.Apps[0].Sponsorship, e.Sponsorship) {
		t.Fatalf("entry = %+v, err = %v", f.Apps, err)
	}
}

func TestCheckRefuses(t *testing.T) {
	for name, c := range map[string]struct {
		edit func(*Entry)
		want string
	}{
		"an upper-case slug":          {func(e *Entry) { e.Slug = "Actual" }, "slug"},
		"a slug with a slash":         {func(e *Entry) { e.Slug = "a/b" }, "slug"},
		"a long slug":                 {func(e *Entry) { e.Slug = strings.Repeat("a", 129) }, "slug"},
		"no name":                     {func(e *Entry) { e.Name = "" }, "name"},
		"a padded name":               {func(e *Entry) { e.Name = " Actual" }, "name"},
		"a two-line name":             {func(e *Entry) { e.Name = "a\nb" }, "name"},
		"a long name":                 {func(e *Entry) { e.Name = strings.Repeat("n", 256) }, "name"},
		"51 keywords":                 {func(e *Entry) { e.Keywords = make([]string, 51) }, "keywords"},
		"an empty tag":                {func(e *Entry) { e.Tags = []string{""} }, "tags[0]"},
		"a long alternative":          {func(e *Entry) { e.AlternativeTo = []string{strings.Repeat("y", 101)} }, "alternative_to[0]"},
		"a long license":              {func(e *Entry) { e.License = strings.Repeat("l", 101) }, "license"},
		"negative stars":              {func(e *Entry) { n := -1; e.Stars = &n }, "stars"},
		"excessive stars":             {func(e *Entry) { n := 1_000_000_001; e.Stars = &n }, "stars"},
		"http license url":            {func(e *Entry) { e.LicenseURL = "http://example.com/license" }, "license_url"},
		"http sponsorship":            {func(e *Entry) { e.Sponsorship = []model.CatalogLink{{Name: "s", URL: "http://example.com/sponsor"}} }, "sponsorship[0].url"},
		"a sponsorship without a url": {func(e *Entry) { e.Sponsorship = []model.CatalogLink{{Name: "s"}} }, "sponsorship[0].url"},
		"an unnamed sponsorship":      {func(e *Entry) { e.Sponsorship = []model.CatalogLink{{URL: "https://example.com/sponsor"}} }, "sponsorship[0].name"},
		"51 sponsorships":             {func(e *Entry) { e.Sponsorship = make([]model.CatalogLink, 51) }, "sponsorship"},
		"an http homepage":            {func(e *Entry) { e.HomepageURL = "http://actualbudget.org" }, "homepage_url"},
		"a javascript logo":           {func(e *Entry) { e.LogoURL = "javascript:alert(1)" }, "logo_url"},
		"a host-less URL":             {func(e *Entry) { e.RepositoryURL = "https:///x" }, "repository_url"},
		"a long URL":                  {func(e *Entry) { e.DocumentationURL = "https://x.test/" + strings.Repeat("p", 2048) }, "documentation_url"},
		"a stale digest":              {func(e *Entry) { e.Files["compose.yml"] += "# edited\n" }, "digest"},
		"no compose file":             {func(e *Entry) { e.Files = map[string]string{"x.conf": ""}; e.Digest = Digest(e.Files) }, "a compose file is required"},
		"a long summary":              {func(e *Entry) { e.Summary = strings.Repeat("s", 201) }, "summary"},
		"a shared volume name": {func(e *Entry) {
			setCompose(e, strings.Replace(budgetCompose, "  data:\nx-", "  data: { name: budget }\nx-", 1))
		}, "would be shared"},
		"an alias bomb": {func(e *Entry) { setCompose(e, aliasBomb()) }, "not valid YAML"},
		"a body over 1 MiB": {func(e *Entry) {
			e.Files["big.txt"] = strings.Repeat("x", template.MaxBodyBytes)
			e.Digest = Digest(e.Files)
		}, "larger than"},
	} {
		t.Run(name, func(t *testing.T) {
			e := entry()
			c.edit(&e)
			_, _, err := e.Check()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to name %q", err, c.want)
			}
		})
	}
}

// setCompose replaces the entry's compose file and its digest.
func setCompose(e *Entry, compose string) {
	e.Files = map[string]string{"compose.yml": compose}
	e.Digest = Digest(e.Files)
}

// aliasBomb is a compose file whose aliases would expand to ten million
// nodes.
func aliasBomb() string {
	out := "services: {app: {image: x}}\nx-a: &a [x, x, x, x, x, x, x, x, x, x]\n"
	prev := "a"
	for _, name := range []string{"b", "c", "d", "e", "f", "g"} {
		out += "x-" + name + ": &" + name + " [" + strings.Repeat("*"+prev+", ", 9) + "*" + prev + "]\n"
		prev = name
	}
	return out
}

// TestGoldenVectors pins the digest and signature formats the catalog
// builder and every installed API must agree on.
func TestGoldenVectors(t *testing.T) {
	const wantDigest = "sha256:d5daca8685846b6a583060be1ae7a6dd26b2d9b9fc20a1d62a9990ce75ad0394"
	got := Digest(map[string]string{"compose.yml": "services: {}\n", "a/b.conf": "x"})
	if got != wantDigest {
		t.Errorf("digest = %q, want %q", got, wantDigest)
	}
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	sig := string(Sign(ed25519.NewKeyFromSeed(seed), []byte(`{"format":1,"apps":[]}`)))
	const wantSig = "T+2V2bD3E4cVQ3oapdAU0RXHLUi1voRfzfsZwS/3uYJo0zUg4mjubwbjBumkDNPoZMqwHRf7mxjFUpeu6BGMAA==\n"
	if sig != wantSig {
		t.Errorf("signature = %q, want %q", sig, wantSig)
	}
}
