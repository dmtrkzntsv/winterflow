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

type route struct {
	handler     http.HandlerFunc
	middlewares []Middleware
}

func NewDispatcher(d Deps) *Dispatcher {
	return &Dispatcher{
		handlers:    make(map[string]map[string]route),
		middlewares: make([]Middleware, 0),
		log:         d.Logger,
		cfg:         config.NewConfig(),
	}
}

type Dispatcher struct {
	handlers    map[string]map[string]route
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

func (d *Dispatcher) Register(method, path string, handler http.HandlerFunc, mws ...Middleware) {
	if d.handlers[path] == nil {
		d.handlers[path] = make(map[string]route)
	}
	// make a copy to avoid external slice mutation
	cp := make([]Middleware, len(mws))
	copy(cp, mws)
	d.handlers[path][method] = route{handler: handler, middlewares: cp}
}

func (d *Dispatcher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	if handlers, exists := d.handlers[path]; exists {
		if rt, exists := handlers[method]; exists {
			var h http.Handler = http.HandlerFunc(rt.handler)
			// apply route-specific middlewares
			for i := len(rt.middlewares) - 1; i >= 0; i-- {
				h = rt.middlewares[i](h)
			}
			// apply global middlewares
			for i := len(d.middlewares) - 1; i >= 0; i-- {
				h = d.middlewares[i](h)
			}
			h.ServeHTTP(w, r)
			return
		}
	}

	http.NotFound(w, r)
}
