package contract_test

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	htpl "winterflow/internal/app/web/handler/templates"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/template"
	tuc "winterflow/internal/domain/usecase/templates"
	"winterflow/internal/infra/db/dbtest"
)

// shopCompose declares, under x-winterflow, an http port, a required NAME
// with a pattern, an optional VERSION (the compose file's "1.0"), a
// required secret, and a sqlite backup; it also references a variable
// nothing declares (SMTP_PASS), which a save warns about.
const shopCompose = `services:
  app:
    image: example/shop:1
    environment:
      NAME: "shop"
      VERSION: "1.0"
      SECRET_KEY: "changeme"
      SMTP_PASS: ${SMTP_PASS}
    volumes: [data:/data]
volumes: { data: {} }
x-winterflow:
  expose:
    - { container: app, port: 8080 }
  variables:
    NAME: { required: true, pattern: "^[a-z]{3,20}$" }
    VERSION: {}
    SECRET_KEY: { required: true, encrypted: true }
  backups:
    - { name: db, type: sqlite, volume: data, path: shop.db }
`

// shopBody is a template body with the compose file and its look.
func shopBody(compose string) template.Body {
	return template.Body{Files: map[string]string{"compose.yaml": compose}, Summary: "A shop", Icon: "cart", Color: "#112233"}
}

// saveTemplate saves a template revision through the API.
func saveTemplate(t *testing.T, c *client, req htpl.SaveTemplateRequest) tuc.SavedTemplate {
	t.Helper()
	var out tuc.SavedTemplate
	c.post(t, "/api/v1/template/save-template", req).expect(t, http.StatusOK).data(t, &out)
	return out
}

// catalogTemplate inserts a catalog template with body, as the catalog
// sync would (catalog_test.go drives the sync itself).
func catalogTemplate(t *testing.T, e *env, id, slug string, body template.Body) {
	t.Helper()
	raw, err := template.Encode(body)
	if err != nil {
		t.Fatal(err)
	}
	meta := &model.CatalogMeta{Summary: body.Summary, Keywords: []string{"store"}, LogoURL: "https://winterflow.io/catalog/logos/" + slug + ".png"}
	if err := e.deps.TemplateRepository.CreateTemplate(e.dbCtx(t), model.Template{ID: id, Source: model.TemplateSourceCatalog, Slug: slug, Name: "Shop", CatalogMeta: meta},
		model.TemplateRevision{Body: raw, Digest: "sha256:" + slug}); err != nil {
		t.Fatal(err)
	}
}

// foreignClient is another org's member (logged in, subscribed) and that
// org's server.
func (e *env) foreignClient(t *testing.T) (*client, string) {
	t.Helper()
	srv := e.foreignServer(t)
	m := e.newClient()
	m.userID, m.email = "", "mallory@other.test"
	m.login(t, m.email, "password")
	m.subscribe(t)
	return m, srv
}

func TestTemplateRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)

	saved := saveTemplate(t, owner, htpl.SaveTemplateRequest{Name: "shop", Body: shopBody(shopCompose), Note: "first"})
	if saved.Revision != 1 || saved.ID == "" || len(saved.Warnings) != 1 || !strings.HasPrefix(saved.Warnings[0], "SMTP_PASS is not a declared variable") {
		t.Fatalf("saved = %+v", saved)
	}
	t.Run("the name is unique in the org", func(t *testing.T) {
		owner.post(t, "/api/v1/template/save-template", htpl.SaveTemplateRequest{Name: "shop", Body: shopBody(shopCompose)}).
			expect(t, http.StatusBadRequest)
	})
	t.Run("an invalid block names the key", func(t *testing.T) {
		bad := strings.Replace(shopCompose, "    VERSION: {}\n", "    VERSION: { default: \"2\" }\n", 1)
		r := owner.post(t, "/api/v1/template/save-template", htpl.SaveTemplateRequest{Name: "other", Body: shopBody(bad)}).expect(t, http.StatusBadRequest)
		if !strings.Contains(string(r.Body), "x-winterflow.variables.VERSION.default") {
			t.Fatalf("body = %s", r.Body)
		}
		shared := strings.Replace(shopCompose, "volumes: { data: {} }", "volumes: { data: { name: shop-data } }", 1)
		r = owner.post(t, "/api/v1/template/save-template", htpl.SaveTemplateRequest{Name: "other", Body: shopBody(shared)}).expect(t, http.StatusBadRequest)
		if !strings.Contains(string(r.Body), "would be shared by every install") {
			t.Fatalf("body = %s", r.Body)
		}
	})
	t.Run("a new revision needs the latest base", func(t *testing.T) {
		next := saveTemplate(t, owner, htpl.SaveTemplateRequest{ID: saved.ID, Name: "shop", Body: shopBody(shopCompose), BaseRevision: 1, Note: "second"})
		if next.Revision != 2 {
			t.Fatalf("next = %+v", next)
		}
		owner.post(t, "/api/v1/template/save-template", htpl.SaveTemplateRequest{ID: saved.ID, Name: "shop", Body: shopBody(shopCompose), BaseRevision: 1}).
			expect(t, http.StatusConflict)
	})
	t.Run("list and read with what the compose file declares", func(t *testing.T) {
		var list tuc.TemplateList
		owner.get(t, "/api/v1/template/get-templates", nil).expect(t, http.StatusOK).data(t, &list)
		if len(list.Templates) != 1 || list.Templates[0].LatestRevision != 2 || list.Templates[0].Instances != 0 || list.Templates[0].Source != "org" ||
			list.Templates[0].Catalog != nil {
			t.Fatalf("list = %+v", list)
		}
		var view tuc.TemplateView
		owner.get(t, "/api/v1/template/get-template", url.Values{"id": {saved.ID}, "revision": {"1"}}).expect(t, http.StatusOK).data(t, &view)
		if view.Revision != 1 || view.Note != "first" || view.Body.Summary != "A shop" || len(view.Revisions) != 2 || view.Catalog != nil {
			t.Fatalf("view = %+v", view)
		}
		d := view.Detected
		if len(d.Variables) != 3 || !d.Variables[2].Encrypted || len(d.Expose) != 1 || d.Expose[0].Target != "app:8080" || len(d.Backups) != 1 {
			t.Fatalf("detected = %+v", d)
		}
		owner.get(t, "/api/v1/template/get-template", url.Values{"id": {saved.ID}, "revision": {"9"}}).expect(t, http.StatusNotFound)
	})
	t.Run("inspect reads a body without saving it", func(t *testing.T) {
		var got tuc.Inspection
		owner.post(t, "/api/v1/template/inspect-template", htpl.InspectTemplateRequest{Body: shopBody(shopCompose)}).expect(t, http.StatusOK).data(t, &got)
		if got.Problem != "" || len(got.Detected.Variables) != 3 || len(got.Detected.Warnings) != 1 {
			t.Fatalf("inspect = %+v", got)
		}
		owner.post(t, "/api/v1/template/inspect-template", htpl.InspectTemplateRequest{Body: shopBody("services: [\n")}).expect(t, http.StatusOK).data(t, &got)
		if !strings.Contains(got.Problem, "not valid YAML") {
			t.Fatalf("inspect = %+v", got)
		}
	})
	t.Run("another org's template is unknown", func(t *testing.T) {
		mallory, _ := e.foreignClient(t)
		mallory.get(t, "/api/v1/template/get-template", url.Values{"id": {saved.ID}}).expect(t, http.StatusNotFound)
		mallory.post(t, "/api/v1/template/delete-template", htpl.TemplateIDRequest{ID: saved.ID}).expect(t, http.StatusNotFound)
		var list tuc.TemplateList
		mallory.get(t, "/api/v1/template/get-templates", nil).expect(t, http.StatusOK).data(t, &list)
		if len(list.Templates) != 0 {
			t.Fatalf("mallory sees %+v", list)
		}
	})
	t.Run("delete", func(t *testing.T) {
		owner.post(t, "/api/v1/template/delete-template", htpl.TemplateIDRequest{ID: saved.ID}).expect(t, http.StatusOK)
		owner.get(t, "/api/v1/template/get-template", url.Values{"id": {saved.ID}}).expect(t, http.StatusNotFound)
	})
}

// Every org reads a catalog template; saving or deleting one is 403.
func TestCatalogTemplatesAreReadOnly(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	cat := dbtest.ID(901)
	catalogTemplate(t, e, cat, "shop", shopBody(shopCompose))
	mallory, _ := e.foreignClient(t)
	for _, c := range []*client{owner, mallory} {
		var list tuc.TemplateList
		c.get(t, "/api/v1/template/get-templates", nil).expect(t, http.StatusOK).data(t, &list)
		if len(list.Templates) != 1 || list.Templates[0].ID != cat || list.Templates[0].Source != "catalog" || list.Templates[0].Slug != "shop" ||
			list.Templates[0].Catalog == nil || list.Templates[0].Catalog.LogoURL != "https://winterflow.io/catalog/logos/shop.png" {
			t.Fatalf("list = %+v", list)
		}
		var view tuc.TemplateView
		c.get(t, "/api/v1/template/get-template", url.Values{"id": {cat}}).expect(t, http.StatusOK).data(t, &view)
		if view.Source != "catalog" || len(view.Detected.Variables) != 3 || view.Catalog == nil || view.Catalog.Summary != "A shop" ||
			len(view.Catalog.Keywords) != 1 || view.Catalog.Tags == nil {
			t.Fatalf("view = %+v", view)
		}
	}
	r := owner.post(t, "/api/v1/template/save-template", htpl.SaveTemplateRequest{ID: cat, Name: "shop", Body: shopBody(shopCompose), BaseRevision: 1}).
		expect(t, http.StatusForbidden)
	if !strings.Contains(string(r.Body), "catalog templates are read-only") {
		t.Fatalf("save body = %s", r.Body)
	}
	mallory.post(t, "/api/v1/template/delete-template", htpl.TemplateIDRequest{ID: cat}).expect(t, http.StatusForbidden)
}

// The browser sees a same-origin image, while only the server reaches the
// fixed catalog logo origin. This also exercises the raw-image OpenAPI 200.
func TestCatalogLogo(t *testing.T) {
	t.Parallel()
	image := []byte("\x89PNG\r\n\x1a\ncontract-logo")
	client := &http.Client{Transport: logoRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://winterflow.io/catalog/logos/shop.png" {
			t.Fatalf("unexpected outbound logo URL: %s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(image)), ContentLength: int64(len(image))}, nil
	})}
	e := newEnvWithLogoClient(t, client)
	owner := e.claim(t)
	id := dbtest.ID(902)
	catalogTemplate(t, e, id, "shop", shopBody(shopCompose))
	r := owner.get(t, "/api/v1/template/catalog-logo", url.Values{"id": {id}}).expect(t, http.StatusOK)
	if !bytes.Equal(r.Body, image) || r.Header.Get("Content-Type") != "image/png" || r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("logo = %d %v %q", r.Status, r.Header, r.Body)
	}
	owner.get(t, "/api/v1/template/catalog-logo", url.Values{"id": {dbtest.ID(903)}}).expect(t, http.StatusNotFound)
	e.anon().get(t, "/api/v1/template/catalog-logo", url.Values{"id": {id}}).expect(t, http.StatusUnauthorized)
}

type logoRoundTrip func(*http.Request) (*http.Response, error)

func (f logoRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
