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

## 4. Configure app domains in winterflow

In each app's **Domains & Routing** card: add the domain, set the upstream
port to the app's published host port, and leave **SSL off**. Cloudflare
already terminates TLS at the edge; with no inbound 80/443, Let's Encrypt
issuance can't complete behind a tunnel, so SSL-on domains would only
produce ACME warnings in the agent log.

## Notes

- Because nothing binds public 80/443, winterflow can run unprivileged:
  set `INGRESS_HTTP_PORT=8081` (for example — `8080` is taken by the UI's
  `API_PORT` default) and point the tunnel there.
- cloudflared reconnects on its own and winterflow restarts it like any
  app. If winterflow itself is down, the tunnel app is still a plain
  compose project: `apps/cloudflared/.winterflow/run.sh up -d` brings it
  up manually.
