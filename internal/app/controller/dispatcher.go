package controller

import (
	"net/http"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

type Middleware func(http.Handler) http.Handler

type Deps struct {
	Logger *logger.Logger
	Cfg    *config.Config
}

func NewDispatcher(d Deps) *Dispatcher {
	return &Dispatcher{
		handlers:    make(map[string]map[string]http.HandlerFunc),
		log:         d.Logger,
		cfg:         config.NewConfig(),
		middlewares: make([]Middleware, 0),
	}
}

type Dispatcher struct {
	handlers    map[string]map[string]http.HandlerFunc
	middlewares []Middleware
	log         *logger.Logger
	cfg         *config.Config
}

func (d *Dispatcher) Use(mws ...Middleware) {
	if d.middlewares == nil {
		d.middlewares = make([]Middleware, 0, len(mws))
	}
	d.middlewares = append(d.middlewares, mws...)
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
			var h http.Handler = http.HandlerFunc(handler)
			for i := len(d.middlewares) - 1; i >= 0; i-- {
				h = d.middlewares[i](h)
			}
			h.ServeHTTP(w, r)
			return
		}
	}

	http.NotFound(w, r)
}
