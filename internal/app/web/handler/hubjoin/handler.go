// Package hubjoin serves the public route a new hub redeems its one-time
// join token at, to get its server certificate.
package hubjoin

import (
	"context"
	"errors"
	"net/http"

	"winterflow/internal/app/web/util"
	"winterflow/internal/domain/usecase/hubjoin"
	"winterflow/pkg/logger"
)

// maxBody bounds the request body (a CSR is ~300 B).
const maxBody = 64 << 10

// Joiner is the slice of *hubjoin.UseCase the route uses.
type Joiner interface {
	Redeem(ctx context.Context, token, csrPEM, publicAddr string) (certPEM, caPEM string, err error)
}

type Handler struct {
	uc  Joiner
	log *logger.Logger
}

type Deps struct {
	Logger *logger.Logger
	Joiner Joiner
}

func NewHandler(d *Deps) *Handler { return &Handler{uc: d.Joiner, log: d.Logger} }

// EnrollRequest is the body of POST /api/v1/hub/enroll.
type EnrollRequest struct {
	Token      string `json:"token"`
	CSR        string `json:"csr"`
	PublicAddr string `json:"public_addr"`
}

// EnrollResponse is the data of the route.
type EnrollResponse struct {
	Certificate   string `json:"certificate"`
	CACertificate string `json:"ca_certificate"`
}

// Enroll handles POST /api/v1/hub/enroll.
func (h *Handler) Enroll(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	req, ok := util.DecodeBody[EnrollRequest](w, r)
	if !ok {
		return
	}
	cert, ca, err := h.uc.Redeem(r.Context(), req.Token, req.CSR, req.PublicAddr)
	switch {
	case err == nil:
		util.Success(w, "hub enrolled", EnrollResponse{Certificate: cert, CACertificate: ca})
	case errors.Is(err, hubjoin.ErrInvalidToken), errors.Is(err, hubjoin.ErrInvalidCSR), errors.Is(err, hubjoin.ErrInvalidAddr):
		util.Error(w, err.Error(), nil)
	default:
		h.log.Error("failed to enroll hub", "error", err)
		util.ErrorStatus(w, http.StatusInternalServerError, "failed to enroll hub")
	}
}
