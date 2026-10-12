package templates

import (
	"net/http"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/command"
	tuc "winterflow/internal/domain/usecase/templates"
)

// waits documents how every instance command answers.
const waits = "Answers 202 with a request_id (the outcome arrives over SSE), or with wait:true the outcome: " +
	"200 with the instance, 502 with the agent's error, or 202 once 10 minutes pass (read get-instance)."

// instanceRoutes are the server-addressed instance routes.
func (h *Handler) instanceRoutes() []api.Route {
	instance := []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusBadGateway}
	read := []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound}
	return []api.Route{
		{
			Method: http.MethodPost, Path: "/api/v1/instance/create-instance", Handler: h.CreateInstance, Auth: api.User,
			Summary: "Create an app from a template revision, or replay a create with the same key", Tag: "instances",
			Description: "The same key with the same request makes no second app: a created instance answers 200, " +
				"a pending one is saved again. A different request is 409. " + waits,
			Request: CreateInstanceRequest{}, Accepted: true, Result: command.SaveAppResponse{}, Response: InstanceResult{},
			Errors: instance,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/instance/get-instance", Handler: h.GetInstance, Auth: api.User,
			Summary: "An instance: template, revision, values (secrets masked), state, status, last error", Tag: "instances",
			Query: InstanceQuery{}, Response: tuc.InstanceView{}, Errors: read,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/instance/get-instances", Handler: h.GetInstances, Auth: api.User,
			Summary: "The org's pending and created instances", Tag: "instances",
			Query: InstancesQuery{}, Response: tuc.InstanceList{}, Errors: read,
		},
	}
}
