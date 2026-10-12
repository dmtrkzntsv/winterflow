package model

import "errors"

// ErrRejected marks a request the user can fix: every sentinel made by
// rejection matches it (an invalid ingress, a domain another app uses, an
// uncovered hostname, an unresolved or reserved storage variable), and the
// HTTP layer answers it with a 400 carrying the error's message.
var ErrRejected = errors.New("rejected")

// rejection is a sentinel error that also matches ErrRejected.
type rejection struct{ msg string }

func (e *rejection) Error() string        { return e.msg }
func (e *rejection) Is(target error) bool { return target == ErrRejected }

func rejected(msg string) error { return &rejection{msg: msg} }

// Rejected makes a sentinel error for another package that matches
// ErrRejected (an invalid backup recipe, say).
func Rejected(msg string) error { return rejected(msg) }
