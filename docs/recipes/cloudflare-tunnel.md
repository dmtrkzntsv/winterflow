# Exposing apps with Cloudflare Tunnel

Run `cloudflared` as a regular winterflow app in front of the embedded Caddy
ingress. No port forwarding, no public IP, no inbound 80/443 on the host —
ideal for a homelab. TLS terminates at the Cloudflare edge; the tunnel carries
traffic to Caddy over an encrypted outbound connection, and Caddy fans out to
your apps by hostname.

```
browser ──TLS──> Cloudflare edge ──tunnel──> cloudflared (winterflow app)
                                                  │ http://host.docker.internal:INGRESS_HTTP_PORT
                                                  ▼
                                       embedded Caddy (winterflow agent)
                                                  │ 127.0.0.1:<published host port>
                                                  ▼
                                             your apps
```

## 1. Create the tunnel

In the Cloudflare Zero Trust dashboard: **Networks → Tunnels → Create a
tunnel** (remotely managed). Copy the tunnel token.

## 2. Create a `cloudflared` app in winterflow

Compose file:

```yaml
services:
  cloudflared:
    image: cloudflare/cloudflared:latest
    restart: unless-stopped
    command: tunnel --no-autoupdate run
    environment:
      - TUNNEL_TOKEN=${TUNNEL_TOKEN}
    extra_hosts:
      - "host.docker.internal:host-gateway"
```

Add a variable named `TUNNEL_TOKEN`, mark it **encrypted**, and paste the
token. It is stored ECIES-encrypted in the app's git history and only
decrypted on the agent at deploy time.

The `extra_hosts` entry is required on Linux: it maps
`host.docker.internal` to the docker host so the container can reach the
Caddy ingress that runs inside the winterflow agent process.

## 3. Route public hostnames to Caddy

In the tunnel's **Public Hostnames**, point each hostname at the same origin:

| Public hostname        | Service                          |
| ---------------------- | -------------------------------- |
| `app1.example.com`     | `http://host.docker.internal:80` |
| `app2.example.com`     | `http://host.docker.internal:80` |
| `flow.example.com`     | `http://host.docker.internal:8080` (the winterflow UI itself, `API_PORT`) |

Use the port you set as `INGRESS_HTTP_PORT`. cloudflared preserves the
original `Host` header, so one origin entry per hostname is enough — Caddy
routes by hostname from there. A wildcard hostname (`*.example.com`) also
works; add the wildcard CNAME pointing at `<tunnel-id>.cfargotunnel.com`
to your DNS zone yourself.

## 4. Add the domain with TLS off

In **Settings → Server · <name> → Domains**, add the names the tunnel
serves — `*.example.com` covers every single-label subdomain — and pick
**Off — behind Cloudflare Tunnel or another proxy**.
Cloudflare already terminates TLS at the edge; with no inbound 80/443,
Let's Encrypt issuance can't complete behind a tunnel, so winterflow serves
these hostnames over plain HTTP only and never asks for a certificate.

## 5. Configure app domains in winterflow

On each app's page, under **Configuration → Domains**, add the hostname
(pick the domain from the suggestions and type the label) and set the
upstream port to the app's published host port. The row shows the
**TLS off** badge it inherits from the domain, and the app's Overview lists
the address as **Behind your proxy** (its link opens `https://`). An
unknown hostname under the wildcard answers a plain 404.

## 6. Let the rate limiter see real visitors

The ingress throttles per client IP (`INGRESS_RATE_LIMIT_RPS`/`_BURST`). Behind
the tunnel every request reaches Caddy from the cloudflared container, so all
visitors would share that one IP — and one bucket. Tell the agent to trust the
tunnel's client-IP header by listing the docker bridge range cloudflared
connects from, in the agent's environment (`.env`):

```
INGRESS_TRUSTED_PROXIES=172.16.0.0/12
```

`172.16.0.0/12` holds Docker's default bridge and compose networks; if your
daemon allocates from another pool, use the subnet `docker network inspect`
shows for the cloudflared app's network. The setting becomes Caddy's own
`trusted_proxies`: a request from a trusted peer is keyed on
`CF-Connecting-IP` (falling back to the rightmost untrusted
`X-Forwarded-For` hop, then the peer). Requests from any other address are
keyed on that address and their headers are ignored, so a direct client
can't forge its way past the limit. Restart the agent after changing it.

The trusted range must contain only proxies that overwrite
`CF-Connecting-IP` with the visitor's address, as cloudflared and
Cloudflare's edge do: a trusted peer can name any client. Caddy also passes
a trusted peer's `X-Forwarded-For`, `X-Forwarded-Proto` and
`X-Forwarded-Host` on to your apps as-is (an untrusted peer's are replaced),
so anything an app decides from them — the client address it logs, whether
the request was HTTPS, the host it builds links for — is only as honest as
every address in the range. `172.16.0.0/12` trusts every container on the
default docker networks, any of which could send its own headers, so don't
run anything there that you don't control or that passes a client's own
`CF-Connecting-IP` through — narrow the range to the cloudflared network's
subnet if you do.

The client-IP lookup is strict: a `CF-Connecting-IP` that itself lies inside
a trusted range is treated as one more proxy hop and skipped, and the client
becomes the next `X-Forwarded-For` hop (else the peer). Visitors whose
address is inside a trusted range therefore share a bucket — another reason
to keep the range narrow.

Using the Cloudflare **proxy** (orange cloud, an A record to this server,
usually with a Cloudflare Origin certificate in **Certificate** mode) instead
of a tunnel? Trust Cloudflare's edge ranges instead — the current list is at
<https://www.cloudflare.com/ips/>; at the time of writing:

```
INGRESS_TRUSTED_PROXIES=173.245.48.0/20,103.21.244.0/22,103.22.200.0/22,103.31.4.0/22,141.101.64.0/18,108.162.192.0/18,190.93.240.0/20,188.114.96.0/20,197.234.240.0/22,198.41.128.0/17,162.158.0.0/15,104.16.0.0/13,104.24.0.0/14,172.64.0.0/13,131.0.72.0/22,2400:cb00::/32,2606:4700::/32,2803:f800::/32,2405:b500::/32,2405:8100::/32,2a06:98c0::/29,2c0f:f248::/32
```

## Notes

- Because nothing binds public 80/443, winterflow can run unprivileged:
  set `INGRESS_HTTP_PORT=8081` (for example — `8080` is taken by the UI's
  `API_PORT` default) and point the tunnel there.
- Other TLS modes, for hosts that don't go through the tunnel: a loaded
  certificate (Certificate mode) also serves any **Auto TLS** hostname it
  matches — Caddy requests no Let's Encrypt certificate for a host a loaded
  certificate already covers. And with Auto TLS an unknown host under a
  wildcard fails the TLS handshake (there is no certificate for it) rather
  than answering the 404, which only plain-HTTP (TLS off) and Certificate
  hosts get.
- cloudflared reconnects on its own and winterflow restarts it like any
  app. If winterflow itself is down, the tunnel app is still a plain
  compose project: `apps/cloudflared/.winterflow/run.sh up -d` brings it
  up manually.
