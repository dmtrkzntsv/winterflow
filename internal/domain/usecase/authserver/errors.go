package authserver

// OAuthError is a protocol error of the public endpoints, rendered as an
// RFC 6749 JSON error ({"error", "error_description"}) rather than the API
// envelope.
type OAuthError struct {
	Status            int
	Code, Description string
}

func (e *OAuthError) Error() string { return e.Code + ": " + e.Description }

// errDisabled answers every public endpoint while the server is off.
var errDisabled = &OAuthError{Status: 404, Code: "invalid_request", Description: "not available"}
