package controller

import (
	"net/http"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

type Deps struct {
	Logger *logger.Logger
	Cfg    *config.Config
}

func NewDispatcher(d Deps) *Dispatcher {
	return &Dispatcher{
		handlers: make(map[string]map[string]http.HandlerFunc),
		log:      d.Logger,
		cfg:      config.NewConfig(),
	}
}

type Dispatcher struct {
	handlers map[string]map[string]http.HandlerFunc
	log      *logger.Logger
	cfg      *config.Config
}

func (d *Dispatcher) Register(method, path string, handler http.HandlerFunc) {
	if d.handlers[path] == nil {
		d.handlers[path] = make(map[string]http.HandlerFunc)
	}
	d.handlers[path][method] = handler
}

func (d *Dispatcher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	if handlers, exists := d.handlers[path]; exists {
		if handler, exists := handlers[method]; exists {
			handler(w, r)
			return
		}
	}

	http.NotFound(w, r)
}
