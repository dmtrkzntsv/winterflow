package templates

import (
	"net/http"

	"winterflow/internal/app/web/api"
	tuc "winterflow/internal/domain/usecase/templates"
)

// Routes is the templates and instances slice of the route table.
func (h *Handler) Routes(api.Guards) []api.Route {
	return append(h.templateRoutes(), h.instanceRoutes()...)
}

// templateRoutes are the org-scoped template routes.
func (h *Handler) templateRoutes() []api.Route {
	org := []int{http.StatusBadRequest, http.StatusNotFound}
	orgConflict := []int{http.StatusBadRequest, http.StatusNotFound, http.StatusConflict}
	return []api.Route{
		{
			Method: http.MethodGet, Path: "/api/v1/template/get-templates", Handler: h.ListTemplates, Auth: api.User,
			Summary: "The org's templates with their instance counts", Tag: "templates",
			Response: tuc.TemplateList{}, Errors: []int{http.StatusBadRequest},
		},
		{
			Method: http.MethodGet, Path: "/api/v1/template/get-template", Handler: h.GetTemplate, Auth: api.User,
			Summary: "A template revision's body (the latest by default) and the revision list", Tag: "templates",
			Query: TemplateQuery{}, Response: tuc.TemplateView{}, Errors: org,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/template/save-template", Handler: h.SaveTemplate, Auth: api.User,
			Summary: "Create a template or add a revision", Tag: "templates",
			Description: "base_revision must be the latest revision (409 otherwise: someone saved meanwhile). " +
				"warnings name file references nothing provides.",
			Request: SaveTemplateRequest{}, Response: tuc.SavedTemplate{}, Errors: orgConflict,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/template/delete-template", Handler: h.DeleteTemplate, Auth: api.User,
			Summary: "Delete a template without live instances", Tag: "templates",
			Request: TemplateIDRequest{}, Errors: orgConflict,
		},
	}
}
