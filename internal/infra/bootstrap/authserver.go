package bootstrap

import (
	"winterflow/internal/domain/usecase/authn"
	"winterflow/internal/domain/usecase/authserver"
	"winterflow/internal/infra/db"
	authserverrepo "winterflow/internal/infra/db/repository/authserver"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// WithAuthServerIssuer replaces the authorization server's issuer, the
// public origin WEB_URL names (tests: "" for a disabled server).
func WithAuthServerIssuer(issuer string) CoreOption {
	return func(o *coreOptions) {
		o.authServerSet = true
		o.authServerIssuer = issuer
	}
}

// newAuthServer builds the OAuth authorization server. It is enabled only
// when WEB_URL is an https origin with no path, or http on loopback:
// refresh tokens, client secrets and bearer tokens must not cross the
// network in the clear, and the discovery documents live at the origin's
// root. Otherwise every OAuth route answers 404 and /mcp takes PATs and
// sessions only.
func newAuthServer(repo *authserverrepo.Repo, tokens *authn.UseCase, conn *db.DB, o coreOptions, cfg *config.ServerConfig, log *logger.Logger) *authserver.UseCase {
	issuer := o.authServerIssuer
	if !o.authServerSet {
		issuer, _ = cfg.GetPublicOrigin()
	}
	if issuer == "" {
		log.Info("OAuth authorization server for MCP clients disabled: WEB_URL is not an https origin (or http on localhost)",
			"web_url", cfg.GetWebURL())
	}
	return authserver.New(repo, tokens, conn.Tx, issuer, nil)
}
