# Upgrading to the opaque-token auth

The release that replaced the old auth library (JWT cookies, go-pkgz/auth)
with opaque sessions, argon2id passwords and built-in sign-in providers
changes a few things for an existing install. Nothing needs to be done for
plain email and password accounts beyond signing in again.

## Everyone signs in once more

Old JWT sessions are not read any more. After the upgrade every browser is
sent to the login page once; the new session is a `wf_session` cookie kept in
the database (30 days from last use, 90 at most), so signing out and "Sign
out everywhere" now end sessions on the server, not only in the browser.

Existing passwords keep working. A bcrypt hash is replaced by an argon2id
one on its owner's next sign-in.

## Personal access tokens keep working

`wfp_…` tokens move to the new `access_tokens` table unchanged (same id, same
hash). Scripts and MCP clients that use one need no change.

## Google sign-in: new redirect URI, https WEB_URL

- The callback moved from `<WEB_URL>/auth/google/callback` to
  `<WEB_URL>/auth/oauth/google/callback`. Add the new URI to the OAuth
  client's "Authorized redirect URIs" in the Google Cloud console (you can
  remove the old one once sign-in works). Until then Google answers
  `redirect_uri_mismatch`.
- Sign-in providers are enabled only when `WEB_URL` is an `https://` origin
  with no path (plain http only on localhost). An install that served the UI
  over plain http on another host loses the Google button until `WEB_URL` is
  https.
- Accounts that only ever signed in with Google have no password, so they
  cannot sign in until both points above are done. If that is the only admin,
  fix the redirect URI before upgrading.
- An account linked by the old library is found on its first Google sign-in
  after the upgrade and re-keyed, so its owner lands in the same account.

## JWT_SECRET stays

Sessions no longer use it, but the API still derives the key that encrypts
alert channel URLs (Org -> Alerts -> Channels) from `JWT_SECRET`. Keep the
value you have: a new one makes every saved channel fail with "cannot
decrypt" until its URL is entered again.
