package server

type Handler struct{}

type Deps struct{}

func NewHandler(d *Deps) *Handler {
	return &Handler{}
}
