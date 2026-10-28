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
  fix the redirect URI before upgrading. Once signed in, such an account can
  add a password in Settings -> Account -> Set a password, so it keeps a way
  in should the provider stop working.
- An account linked by the old library is found on its first Google sign-in
  after the upgrade and re-keyed, so its owner lands in the same account.

## JWT_SECRET is renamed to ENCRYPTION_KEY

Sessions no longer use it; the only thing it still keys is the encryption of
alert channel URLs (Org -> Alerts -> Channels), so it is now called
`ENCRYPTION_KEY`. Keep the value you have: a new one makes every saved
channel fail with "cannot decrypt" until its URL is entered again.

- Installer installs: rerun the installer. `install.sh` renames the
  `JWT_SECRET=` line of `/etc/winterflow/winterflow.env` to `ENCRYPTION_KEY=`
  with the same value; `install-control-plane.sh` moves the kept secret to
  `ENCRYPTION_KEY` in its state file and the API env file.
- Installing or rolling back to an older release with `--version` writes a
  `JWT_SECRET` line too, with the same value and the comment "read only by
  releases before the ENCRYPTION_KEY rename; safe to remove after
  upgrading": those releases sign sessions with `JWT_SECRET` and would
  accept an empty one. A later rerun without `--version` removes that line.
- Manual installs: rename the line in your env file
  (`JWT_SECRET=...` -> `ENCRYPTION_KEY=...`), keeping the value.
- Until you do, the old name still works: with `ENCRYPTION_KEY` unset, a set
  `JWT_SECRET` is used (the derived key is identical, so stored channels keep
  decrypting) and the API logs "JWT_SECRET is deprecated; rename it to
  ENCRYPTION_KEY" at startup. With both set to different values,
  `ENCRYPTION_KEY` wins and the API warns that `JWT_SECRET` is ignored. The
  fallback will be removed in a later release.
