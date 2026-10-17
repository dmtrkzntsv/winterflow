package templates

import (
	"net/http"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/model"
	tuc "winterflow/internal/domain/usecase/templates"
)

// Routes is the templates and instances slice of the route table.
func (h *Handler) Routes(api.Guards) []api.Route {
	return append(h.templateRoutes(), h.instanceRoutes()...)
}

// templateRoutes are the org-scoped template routes.
func (h *Handler) templateRoutes() []api.Route {
	org := []int{http.StatusBadRequest, http.StatusNotFound}
	own := []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict}
	orgConflict := []int{http.StatusBadRequest, http.StatusNotFound, http.StatusConflict}
	return []api.Route{
		{
			Method: http.MethodGet, Path: "/api/v1/template/get-templates", Handler: h.ListTemplates, Auth: api.User,
			Summary: "The org's templates, then the catalog's, with the org's instance counts", Tag: "templates",
			Response: tuc.TemplateList{}, Errors: []int{http.StatusBadRequest},
		},
		{
			Method: http.MethodGet, Path: "/api/v1/template/get-template", Handler: h.GetTemplate, Auth: api.User,
			Summary: "A template revision's body (the latest by default), what its compose files declare, and the revision list", Tag: "templates",
			Query: TemplateQuery{}, Response: tuc.TemplateView{}, Errors: org,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/template/catalog-logo", Handler: h.CatalogLogo, Auth: api.User,
			Summary: "The current logo of a catalog template, served from this origin", Tag: "templates",
			Description: "Only the current catalog logo at winterflow.io/catalog/logos is fetched. Missing, unsafe, or unavailable images answer 404.",
			Query:       CatalogLogoQuery{}, Binary: true, ContentType: "image/*", Errors: org,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/template/save-template", Handler: h.SaveTemplate, Auth: api.User,
			Summary: "Create an org template or add a revision", Tag: "templates",
			Description: "The compose files' x-winterflow block declares the variables, exposed ports and backups. " +
				"base_revision must be the latest revision (409 otherwise: someone saved meanwhile). " +
				"warnings name a variable nothing uses and file references nothing provides. A catalog template is read-only (403).",
			Request: SaveTemplateRequest{}, Response: tuc.SavedTemplate{}, Errors: own,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/template/inspect-template", Handler: h.InspectTemplate, Auth: api.User,
			Summary: "What a template body declares and the first problem a save would refuse", Tag: "templates",
			Description: "Stores nothing: the editor's Detected panel follows the files with it. A body a save would refuse " +
				"answers 200 with the problem.",
			Request: InspectTemplateRequest{}, Response: tuc.Inspection{}, Errors: []int{http.StatusBadRequest},
		},
		{
			Method: http.MethodPost, Path: "/api/v1/template/delete-template", Handler: h.DeleteTemplate, Auth: api.User,
			Summary: "Delete an org template without live instances", Tag: "templates",
			Description: "A catalog template is read-only (403).",
			Request:     TemplateIDRequest{}, Errors: own,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/template/upgrade-instances", Handler: h.UpgradeInstances, Auth: api.User,
			Summary: "Upgrade all of a template's instances, one app at a time", Tag: "templates",
			Description: "Queues a template_upgrade rollout: each created instance below the revision is re-rendered with " +
				"its values and redeployed, oldest first; one that needs new values is skipped; the first failure stops it. " +
				"409 while an upgrade of the template is active. Progress arrives as rollout events over SSE.",
			Request: UpgradeInstancesRequest{}, Response: model.RolloutView{}, Errors: orgConflict,
		},
	}
}
