// Package enroll serves the public agent enrollment routes: an agent posts
// its CSR, shows the returned code, and polls until a user claims it.
package enroll

import (
	"context"
	"errors"
	"net/http"
	"time"

	"winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/usecase/enrollment"
	"winterflow/pkg/logger"
)

const (
	// maxBody bounds the public routes' request bodies (a CSR is ~300 B).
	maxBody = 64 << 10
	// maxHostname is the longest valid DNS name.
	maxHostname = 253
)

// Enrollments is the slice of *enrollment.UseCase the routes use.
type Enrollments interface {
	Start(ctx context.Context, csrPEM, hostname string) (enrollment.StartResult, error)
	Refresh(ctx context.Context, pollSecret string) (enrollment.StartResult, error)
	Status(ctx context.Context, pollSecret string) (enrollment.Status, error)
}

type Handler struct {
	uc  Enrollments
	log *logger.Logger
}

type Deps struct {
	Logger      *logger.Logger
	Enrollments Enrollments
}

func NewHandler(d *Deps) *Handler { return &Handler{uc: d.Enrollments, log: d.Logger} }

// StartRequest is the body of POST /api/v1/agent/enroll.
type StartRequest struct {
	CSR      string `json:"csr"`
	Hostname string `json:"hostname"`
}

// PollRequest is the body of the refresh and status routes.
type PollRequest struct {
	PollSecret string `json:"poll_secret"`
}

// CodeResponse is the data of the start and refresh routes.
type CodeResponse struct {
	EnrollmentID string    `json:"enrollment_id"`
	PollSecret   string    `json:"poll_secret"`
	Code         string    `json:"code"`
	ExpiresAt    time.Time `json:"expires_at"`
	CodesLeft    int       `json:"codes_left"`
}

// StatusResponse is the data of the status route. State is pending, expired,
// failed, claimed or uncollected (claimed, but the certificate was not
// collected within 15 minutes); the claim fields are set once claimed.
type StatusResponse struct {
	State         string     `json:"state"`
	Code          string     `json:"code,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	CodesLeft     int        `json:"codes_left"`
	ServerID      string     `json:"server_id,omitempty"`
	Certificate   string     `json:"certificate,omitempty"`
	CACertificate string     `json:"ca_certificate,omitempty"`
	HubAddr       string     `json:"hub_addr,omitempty"`
}

// decode reads a size-capped JSON body.
func decode[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	return util.DecodeBody[T](w, r)
}

func (h *Handler) fail(w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, enrollment.ErrInvalidCSR):
		util.Error(w, err.Error(), nil)
	case errors.Is(err, model.ErrNotFound):
		util.Error(w, "unknown enrollment", nil)
	case errors.Is(err, enrollment.ErrNoCodesLeft):
		util.ErrorStatus(w, http.StatusConflict, "no codes left")
	case errors.Is(err, enrollment.ErrTooManyPending):
		util.ErrorStatus(w, http.StatusServiceUnavailable, "too many pending enrollments")
	default:
		h.log.Error(what, "error", err)
		util.ErrorStatus(w, http.StatusInternalServerError, what)
	}
}

func codeResponse(r enrollment.StartResult) CodeResponse {
	return CodeResponse{EnrollmentID: r.EnrollmentID, PollSecret: r.PollSecret, Code: r.Code, ExpiresAt: r.ExpiresAt, CodesLeft: r.CodesLeft}
}

// Start handles POST /api/v1/agent/enroll.
func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[StartRequest](w, r)
	if !ok {
		return
	}
	if len(req.Hostname) > maxHostname {
		util.Error(w, "hostname is too long", nil)
		return
	}
	res, err := h.uc.Start(r.Context(), req.CSR, req.Hostname)
	if err != nil {
		h.fail(w, err, "failed to start enrollment")
		return
	}
	util.Success(w, "enrollment started", codeResponse(res))
}

// Refresh handles POST /api/v1/agent/enroll/refresh.
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[PollRequest](w, r)
	if !ok {
		return
	}
	res, err := h.uc.Refresh(r.Context(), req.PollSecret)
	if err != nil {
		h.fail(w, err, "failed to refresh the code")
		return
	}
	util.Success(w, "code refreshed", codeResponse(res))
}

// Status handles POST /api/v1/agent/enroll/status.
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[PollRequest](w, r)
	if !ok {
		return
	}
	st, err := h.uc.Status(r.Context(), req.PollSecret)
	if err != nil {
		h.fail(w, err, "failed to read the enrollment")
		return
	}
	out := StatusResponse{State: st.State, Code: st.Code, CodesLeft: st.CodesLeft, ServerID: st.ServerID,
		Certificate: st.Certificate, CACertificate: st.CACertificate, HubAddr: st.HubAddr}
	if !st.ExpiresAt.IsZero() {
		out.ExpiresAt = &st.ExpiresAt
	}
	util.Success(w, "ok", out)
}
