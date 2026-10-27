package oauthlogin

import (
	"context"
	"errors"

	"winterflow/internal/domain/model"
)

// StartRequest begins a sign-in (Intent model.IntentLogin) or a link of a
// provider account to the signed-in user LinkUserID (model.IntentLink).
// ReturnTo is where the browser lands afterwards (SafeReturnTo).
type StartRequest struct {
	Provider   string
	Intent     string
	ReturnTo   string
	LinkUserID string
}

// StartResult is the provider's authorization URL to send the browser to
// and the flow secret the browser must keep (a cookie) for the callback.
type StartResult struct {
	URL  string
	Flow string
}

// Start stores the state and returns the authorization URL.
func (s *Service) Start(ctx context.Context, req StartRequest) (StartResult, error) {
	p, err := s.provider(req.Provider)
	if err != nil {
		return StartResult{}, err
	}
	switch req.Intent {
	case model.IntentLogin:
		req.LinkUserID = ""
	case model.IntentLink:
		if req.LinkUserID == "" {
			return StartResult{}, errors.New("oauthlogin: a link needs the signed-in user")
		}
	default:
		return StartResult{}, model.Rejected("mode must be login or link")
	}
	var state, nonce, flow, verifier, challenge string
	for _, v := range []*string{&state, &nonce, &flow} {
		if *v, err = randomToken(); err != nil {
			return StartResult{}, err
		}
	}
	if p.Client.UsesPKCE() {
		if verifier, err = randomToken(); err != nil {
			return StartResult{}, err
		}
		challenge = pkceChallenge(verifier)
	}
	now := s.now()
	if err := s.sweepExpired(ctx, now); err != nil {
		return StartResult{}, err
	}
	if err := s.repo.InsertLoginState(ctx, model.LoginState{
		StateHash: sha256Hex(state), Provider: p.ID, Nonce: nonce, CodeVerifier: verifier,
		FlowHash: sha256Hex(flow), Intent: req.Intent, ReturnTo: SafeReturnTo(req.ReturnTo), LinkUserID: req.LinkUserID,
		CreatedAt: now, ExpiresAt: now.Add(StateTTL),
	}); err != nil {
		return StartResult{}, err
	}
	u, err := p.Client.AuthURL(ctx, state, nonce, challenge, s.RedirectURI(p.ID))
	if err != nil {
		return StartResult{}, err
	}
	return StartResult{URL: u, Flow: flow}, nil
}
