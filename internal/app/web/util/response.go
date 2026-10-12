package util

import (
	"encoding/json"
	"errors"
	"net/http"

	"winterflow/internal/domain/model"
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
