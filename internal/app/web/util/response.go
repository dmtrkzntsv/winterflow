package util

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"winterflow/internal/domain/model"
	"winterflow/pkg/logger"
)

type APIResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data"`
}

// write writes the JSON envelope with status.
func write(w http.ResponseWriter, status int, success bool, message string, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(APIResponse{Success: success, Message: message, Data: data})
}

func Success(w http.ResponseWriter, message string, data interface{}) {
	write(w, http.StatusOK, true, message, data)
}

// Accepted responds 202 for fire-and-forward commands: the request was
// accepted and its result will arrive asynchronously over SSE, correlated by
// the request_id in data.
func Accepted(w http.ResponseWriter, message string, data interface{}) {
	write(w, http.StatusAccepted, true, message, data)
}

func Error(w http.ResponseWriter, message string, data interface{}) {
	write(w, http.StatusBadRequest, false, message, data)
}

// ErrorStatus writes a JSON error with the given status (404 an unknown
// resource, 409 a conflict, 502 an agent's failure).
func ErrorStatus(w http.ResponseWriter, status int, message string) {
	write(w, status, false, message, nil)
}

// StatusFor is the status an error class answers with: model.ErrNotFound
// 404, model.ErrConflict 409, anything else (model.ErrRejected included)
// 400.
func StatusFor(err error) int {
	switch {
	case errors.Is(err, model.ErrRejected):
		return http.StatusBadRequest
	case errors.Is(err, model.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, model.ErrConflict):
		return http.StatusConflict
	}
	return http.StatusBadRequest
}

// Fail answers a usecase error: a classified one (model.ErrNotFound,
// ErrConflict, ErrRejected) with its status (StatusFor) and message,
// anything else logged ("<area>: <what>") and answered with a generic 400
// "failed to <what>".
func Fail(w http.ResponseWriter, log *logger.Logger, area string, err error, what string) {
	if errors.Is(err, model.ErrNotFound) || errors.Is(err, model.ErrConflict) || errors.Is(err, model.ErrRejected) {
		ErrorStatus(w, StatusFor(err), err.Error())
		return
	}
	log.Error(area+": "+what, "error", err)
	Error(w, "failed to "+what, nil)
}

// RequireUUID answers 400 with msg unless id is a canonical UUID (the ids
// of templates, rollouts and orgs; on Postgres anything else could not
// even be looked up).
func RequireUUID(w http.ResponseWriter, id, msg string) bool {
	if u, err := uuid.Parse(id); err != nil || u.String() != id {
		Error(w, msg, nil)
		return false
	}
	return true
}
