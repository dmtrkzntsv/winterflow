package authserver

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"winterflow/internal/domain/model"
)

const maxState = 1024

// redirectErr marks a bad request whose redirect target is verified, so the
// error goes back to the client; any other error is shown to the user and
// never redirected. dropState keeps a rejected state value out of the
// redirect.
type redirectErr struct {
	code, desc string
	dropState  bool
}

// validate checks an authorization request: an untrusted client or
// redirect is an error; a bad OAuth parameter with a trusted redirect is a
// redirectErr.
func (u *UseCase) validate(ctx context.Context, req model.AuthorizationRequest) (model.OAuthClient, *redirectErr, error) {
	if !u.Enabled() {
		return model.OAuthClient{}, nil, model.ErrAuthServerDisabled
	}
	// Client ids are UUIDs; anything else could not even be looked up on
	// Postgres.
	if id, err := uuid.Parse(req.ClientID); err != nil || id.String() != req.ClientID {
		return model.OAuthClient{}, nil, model.ErrOAuthClientUnknown
	}
	c, err := u.repo.GetClient(ctx, req.ClientID)
	if errors.Is(err, model.ErrNotFound) {
		return model.OAuthClient{}, nil, model.ErrOAuthClientUnknown
	}
	if err != nil {
		return model.OAuthClient{}, nil, err
	}
	if !MatchRedirectURI(c.RedirectURIs, req.RedirectURI) {
		return model.OAuthClient{}, nil, model.ErrOAuthRedirect
	}
	switch {
	case req.ResponseType != "code":
		return c, &redirectErr{code: "unsupported_response_type", desc: "response_type must be code"}, nil
	case req.CodeChallengeMethod != "S256" || !validChallenge(req.CodeChallenge):
		return c, &redirectErr{code: "invalid_request", desc: "PKCE with S256 is required"}, nil
	case len(req.State) > maxState:
		return c, &redirectErr{code: "invalid_request", desc: "state is too long", dropState: true}, nil
	case !u.resourceOK(req.Resource):
		return c, &redirectErr{code: "invalid_target", desc: "unknown resource"}, nil
	}
	return c, nil, nil
}

// validChallenge: an S256 challenge is the unpadded base64url of a SHA-256
// digest, 43 characters.
func validChallenge(c string) bool {
	if len(c) != 43 {
		return false
	}
	for i := 0; i < len(c); i++ {
		b := c[i]
		if !(b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-' || b == '_') {
			return false
		}
	}
	return true
}

// resourceOK accepts no resource indicator or /mcp (RFC 8707).
func (u *UseCase) resourceOK(r string) bool {
	return r == "" || strings.TrimRight(r, "/") == u.ResourceURL()
}

func (u *UseCase) redirectWith(base string, params url.Values) string {
	ru, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := ru.Query()
	for k, v := range params {
		q[k] = v
	}
	q.Set("iss", u.issuer)
	ru.RawQuery = q.Encode()
	return ru.String()
}

func (u *UseCase) errorRedirect(req model.AuthorizationRequest, re redirectErr) string {
	p := url.Values{"error": {re.code}}
	if re.desc != "" {
		p.Set("error_description", re.desc)
	}
	if req.State != "" && !re.dropState {
		p.Set("state", req.State)
	}
	return u.redirectWith(req.RedirectURI, p)
}

// DescribeAuthorization is what the consent page shows for req.
func (u *UseCase) DescribeAuthorization(ctx context.Context, req model.AuthorizationRequest) (model.AuthorizationDescription, error) {
	c, re, err := u.validate(ctx, req)
	if err != nil {
		return model.AuthorizationDescription{}, err
	}
	host, loopback := RedirectHost(req.RedirectURI)
	res := model.AuthorizationDescription{ClientName: c.Name, RedirectHost: host, IsLoopback: loopback}
	if re != nil {
		res.ErrorRedirectURL = u.errorRedirect(req, *re)
	}
	return res, nil
}

// ApproveAuthorization issues a one-shot code for the user and returns the
// URL the browser goes to (the client's redirect with code, state, iss).
func (u *UseCase) ApproveAuthorization(ctx context.Context, userID string, req model.AuthorizationRequest) (string, error) {
	c, re, err := u.validate(ctx, req)
	if err != nil {
		return "", err
	}
	if re != nil {
		return u.errorRedirect(req, *re), nil
	}
	raw, hash, err := newSecret("")
	if err != nil {
		return "", err
	}
	now := u.now().UTC()
	err = u.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := u.repo.InsertCode(ctx, model.OAuthCode{
			CodeHash: hash, ClientID: c.ID, UserID: userID,
			RedirectURI: req.RedirectURI, CodeChallenge: req.CodeChallenge, Resource: u.ResourceURL(),
			CreatedAt: now, ExpiresAt: now.Add(CodeTTL),
		}); err != nil {
			return err
		}
		return u.repo.MarkClientUsed(ctx, c.ID, now)
	})
	if err != nil {
		return "", err
	}
	p := url.Values{"code": {raw}}
	if req.State != "" {
		p.Set("state", req.State)
	}
	return u.redirectWith(req.RedirectURI, p), nil
}

// DeclineAuthorization returns the URL that sends access_denied back to the
// client.
func (u *UseCase) DeclineAuthorization(ctx context.Context, req model.AuthorizationRequest) (string, error) {
	_, re, err := u.validate(ctx, req)
	if err != nil {
		return "", err
	}
	if re != nil {
		return u.errorRedirect(req, *re), nil
	}
	return u.errorRedirect(req, redirectErr{code: "access_denied"}), nil
}
