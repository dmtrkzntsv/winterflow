// Registered domains: a TS mirror of internal/domain/model/domain.go (the Go
// rule is authoritative; this one drives suggestions and inline hints).

export type DomainTLS = "auto" | "certificate" | "off";

export type RegisteredDomain = {
  name: string;
  tls: DomainTLS;
  not_after?: string; // RFC 3339, certificate mode
  issuer?: string; // CN of the issuer, certificate mode
  apps: string[] | null; // app ids using a covered host
};

export const TLS_LABELS: Record<DomainTLS, string> = {
  auto: "Auto TLS",
  certificate: "Certificate",
  off: "TLS off",
};

// Same strict lowercase RFC-1123 rule as the ingress hostnames.
const HOSTNAME_RE =
  /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$/;

export function isWildcard(name: string): boolean {
  return name.startsWith("*.");
}

// domainNameError mirrors model.ValidateDomainName; "" means valid.
export function domainNameError(name: string): string {
  const host = isWildcard(name) ? name.slice(2) : name;
  if (name.length > 253 || !HOSTNAME_RE.test(host)) {
    return "Use a lowercase hostname like example.com, or *.example.com for every subdomain.";
  }
  return "";
}

// covers mirrors model.DomainCovers: an exact name covers itself; "*.s"
// covers "<one label>.s", not "s" and not "a.b.s". Case-insensitive.
export function covers(name: string, host: string): boolean {
  const n = name.toLowerCase();
  const h = host.toLowerCase();
  if (!isWildcard(n)) return n === h;
  const dot = h.indexOf(".");
  return dot > 0 && h.slice(dot + 1) === n.slice(2);
}

// winningDomain mirrors model.WinningDomain: the covering registration, an
// exact name winning over a wildcard; null when none covers host.
export function winningDomain(names: string[], host: string): string | null {
  let wildcard: string | null = null;
  for (const n of names) {
    if (!covers(n, host)) continue;
    if (!isWildcard(n)) return n;
    wildcard = n;
  }
  return wildcard;
}

export type CertExpiry = "ok" | "soon" | "expired";

// certExpiry grades a certificate's not_after: expired, under 30 days
// ("soon"), or fine. null without a date.
export function certExpiry(notAfter: string | undefined, now: Date = new Date()): CertExpiry | null {
  if (!notAfter) return null;
  const left = new Date(notAfter).getTime() - now.getTime();
  if (Number.isNaN(left)) return null;
  if (left <= 0) return "expired";
  return left < 30 * 24 * 60 * 60 * 1000 ? "soon" : "ok";
}
