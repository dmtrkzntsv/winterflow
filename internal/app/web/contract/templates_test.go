package contract_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	htpl "winterflow/internal/app/web/handler/templates"
	"winterflow/internal/domain/template"
	tuc "winterflow/internal/domain/usecase/templates"
)

// shopCompose publishes the port the ingress row routes to, references a
// secret parameter and a shared variable, and declares a sqlite backup.
const shopCompose = `services:
  app:
    image: example/shop:${VERSION}
    ports: ["127.0.0.1:${PORT}:8080"]
    environment:
      SECRET_KEY: ${SECRET_KEY}
      SMTP_PASS: ${SMTP_PASS}
    volumes: [data:/data]
volumes: { data: {} }
x-winterflow:
  backups:
    - { name: db, type: sqlite, volume: data, path: shop.db }
`

func strPtr(s string) *string { return &s }

// shopBody is a template with a plain, a defaulted and a secret parameter,
// an ingress row from parameters and a backup default on storage "disk".
func shopBody(compose string) template.Body {
	return template.Body{
		Files: map[string]string{"compose.yaml": compose},
		Parameters: []template.Parameter{
			{Name: "NAME", Required: true, Pattern: "^[a-z]{3,20}$"},
			{Name: "PORT", Required: true, Pattern: "^[0-9]{2,5}$"},
			{Name: "VERSION", Default: strPtr("1.0")},
			{Name: "SECRET_KEY", Secret: true, Required: true},
		},
		AppName: "shop-${NAME}",
		Icon:    "cart",
		Color:   "#112233",
		Ingress: []template.IngressRow{{Host: "${NAME}.example.test", Service: "app", Port: "${PORT}"}},
		Backups: map[string]template.BackupDefault{"db": {Enabled: true, Storage: "disk"}},
	}
}

// saveTemplate saves a template revision through the API.
func saveTemplate(t *testing.T, c *client, req htpl.SaveTemplateRequest) tuc.SavedTemplate {
	t.Helper()
	var out tuc.SavedTemplate
	c.post(t, "/api/v1/template/save-template", req).expect(t, http.StatusOK).data(t, &out)
	return out
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
	if saved.Revision != 1 || saved.ID == "" || len(saved.Warnings) != 1 || !strings.HasPrefix(saved.Warnings[0], "SMTP_PASS is not a parameter") {
		t.Fatalf("saved = %+v", saved)
	}
	t.Run("the name is unique in the org", func(t *testing.T) {
		owner.post(t, "/api/v1/template/save-template", htpl.SaveTemplateRequest{Name: "shop", Body: shopBody(shopCompose)}).
			expect(t, http.StatusBadRequest)
	})
	t.Run("an invalid body names the field", func(t *testing.T) {
		bad := shopBody(shopCompose)
		bad.Parameters[3].Default = strPtr("plaintext")
		r := owner.post(t, "/api/v1/template/save-template", htpl.SaveTemplateRequest{Name: "other", Body: bad}).expect(t, http.StatusBadRequest)
		if !strings.Contains(string(r.Body), "parameters[3].default") {
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
	t.Run("list and read", func(t *testing.T) {
		var list tuc.TemplateList
		owner.get(t, "/api/v1/template/get-templates", nil).expect(t, http.StatusOK).data(t, &list)
		if len(list.Templates) != 1 || list.Templates[0].LatestRevision != 2 || list.Templates[0].Instances != 0 {
			t.Fatalf("list = %+v", list)
		}
		var view tuc.TemplateView
		owner.get(t, "/api/v1/template/get-template", url.Values{"id": {saved.ID}, "revision": {"1"}}).expect(t, http.StatusOK).data(t, &view)
		if view.Revision != 1 || view.Note != "first" || view.Body.AppName != "shop-${NAME}" || view.Body.Ingress[0].Port != "${PORT}" || len(view.Revisions) != 2 {
			t.Fatalf("view = %+v", view)
		}
		owner.get(t, "/api/v1/template/get-template", url.Values{"id": {saved.ID}, "revision": {"9"}}).expect(t, http.StatusNotFound)
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
