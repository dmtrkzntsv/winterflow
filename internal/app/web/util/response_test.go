package util

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"winterflow/internal/domain/model"
)

func TestStatusFor(t *testing.T) {
	for err, want := range map[error]int{
		model.Rejected("bad"):                               http.StatusBadRequest,
		model.ErrTemplateNotFound:                           http.StatusNotFound,
		model.ErrKeyInUse:                                   http.StatusConflict,
		fmt.Errorf("wrapped: %w", model.ErrCatalogReadOnly): http.StatusForbidden,
		errors.New("unclassified"):                          http.StatusBadRequest,
	} {
		if got := StatusFor(err); got != want {
			t.Errorf("%v: %d, want %d", err, got, want)
		}
	}
}
