package controller

import (
	"net/http"
	"winterflow/internal/app/controller/util"
)

func (d *Dispatcher) RegisterRoutes() {
	d.Register("GET", "/_/health", func(w http.ResponseWriter, r *http.Request) {
		util.Success(w, "healthy", nil)
	})

	d.Register("GET", "/api/v1/test/test-method", func(w http.ResponseWriter, r *http.Request) {
		util.Success(w, "TEST METHOD OK", nil)
	})
}
