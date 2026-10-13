package model

import "errors"

// Error classes the HTTP layer maps to a status, answering with the
// error's message. ErrRejected marks a request the user can fix (400): an
// invalid ingress, a domain another app uses, an uncovered hostname, an
// unresolved or reserved storage variable. ErrNotFound marks an unknown
// template, revision or instance key, or one of another org (404).
// ErrConflict marks a request the current state refuses (409).
var (
	ErrRejected = errors.New("rejected")
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

// classified is a sentinel error that also matches its class.
type classified struct {
	msg   string
	class error
}

func (e *classified) Error() string        { return e.msg }
func (e *classified) Is(target error) bool { return target == e.class }

func rejected(msg string) error { return &classified{msg, ErrRejected} }

// Rejected makes a sentinel error for another package that matches
// ErrRejected (an invalid backup recipe, say).
func Rejected(msg string) error { return rejected(msg) }
