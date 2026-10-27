# Connect Claude or Codex to Winterflow (MCP)

Winterflow serves an [MCP](https://modelcontextprotocol.io) endpoint at
`<your Winterflow URL>/mcp`, in standalone and in the distributed API. An
agent connected to it can list your servers and apps, write and save compose
apps, start, stop and roll them back, read their status and logs, and manage
the server's registered domains: the deploy loop you would otherwise click
through in the UI.

There are two ways to connect: sign in through the browser (OAuth, nothing to
copy), or send a personal access token.

## Prerequisites

- For the browser sign-in, `WEB_URL` must be the public https origin of your
  instance, with no path (for example `https://winterflow.example.com`; a URL
  with a path such as `https://example.com/winterflow` does not work, because
  discovery and the sign-in endpoints are served from the root). Plain
  `http://` is accepted only for `localhost`, `127.0.0.1` and `[::1]`. With
  any other `WEB_URL` the sign-in is off and `/mcp` accepts personal access
  tokens (and the browser session) only.
- Behind a reverse proxy or a tunnel, pass `/mcp`, `/.well-known/*` and
  `/oauth/*` through to Winterflow, not only `/api`.
- Claude on the web, desktop and mobile connects from Anthropic's servers, so
  the instance must be reachable from the internet. Claude Code and Codex run
  on your computer and only need to reach the URL from there.

## Claude Code

With the browser sign-in:

```bash
claude mcp add --transport http winterflow https://<your Winterflow URL>/mcp
```

Then run `/mcp` inside Claude Code, pick `winterflow` and choose
Authenticate. The browser opens Winterflow's approval page; sign in if you
are not already and press Allow.

With a personal access token (create one in Settings → API tokens; it starts
with `wfp_`):

```bash
claude mcp add --transport http winterflow https://<your Winterflow URL>/mcp \
  --header "Authorization: Bearer wfp_..."
```

## Claude (claude.ai, desktop, mobile)

1. Settings, Connectors, Add custom connector.
2. Enter `https://<your Winterflow URL>/mcp` and add it.
3. Connect: the browser opens Winterflow. Sign in, check the app name and
   where it will send you back, and press Allow.

## Codex

With the browser sign-in:

```bash
codex mcp add winterflow --url https://<your Winterflow URL>/mcp
codex mcp login winterflow
```

With a personal access token, in `~/.codex/config.toml`:

```toml
[mcp_servers.winterflow]
url = "https://<your Winterflow URL>/mcp"
bearer_token_env_var = "WINTERFLOW_TOKEN"
```

and `export WINTERFLOW_TOKEN=wfp_...` in the shell Codex runs from. Flags and
keys can change between Codex releases; `codex mcp add --help` shows the
current ones.

## What approving gives the app

The consent page names the app and the host it sends you back to, and says
what it gets: full access to your organization's servers and apps, the same
as you have in the UI. The app never sees your password, and its token works
only on `/mcp`, not on the rest of the API.

The access token lasts one hour and the app renews it with a refresh token
that changes on every use. A connection expires 90 days after its last
refresh, and the app then asks you to sign in again.

Secret values never pass through MCP: the agent sends a secret variable only
as `<encrypted>` (keep the stored value) and asks you for new ones through a
secret request link, which you open in Winterflow while signed in.

## Revoking

Settings → Connected apps lists every app you approved, with when it was
connected and last used. Disconnect signs it out at once: its next call is
refused and it has to be approved again. A personal access token is deleted
in Settings → API tokens. Removing a member from the organization, or deleting
the account, ends all of their sessions, tokens and connected apps.

## Limits

Each user may make about 10 calls a second to `/mcp` (bursts of 30); past
that the answer is 429. Calls without a valid credential share the per-IP
limit of the sign-in routes.

A tool that runs a command on a server (saving an app, reading its logs)
waits up to 50 seconds for the result, so the call stays below the idle
timeout of tunnels and proxies. A command still running then answers
`{"status": "running", "request_id": ...}`; the agent calls
`get_command_result` with that id to wait for it again.

## Troubleshooting

- The client says the server does not support authorization, or never opens
  a browser: check that `WEB_URL` is the exact address the client uses
  (https, no path, same host) and that
  `https://<your Winterflow URL>/.well-known/oauth-authorization-server`
  returns JSON. A personal access token works without any of this.
- 401 with a token: the token was deleted, expired, or (for an OAuth token)
  the app was disconnected or `WEB_URL` changed. Sign in again or create a new
  token.
