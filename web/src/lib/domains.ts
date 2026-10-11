// Registered domains. Coverage (which registration wins a hostname) is the
// API's job (GET /api/v1/domains/check); this file holds the shapes, the
// mode wording and the syntax rules that give instant feedback.

export type DomainTLS = "auto" | "certificate" | "off";

// RegisteredDomain is a registration as the API's index knows it
// (get-domains, and the covering one in a domains/check answer).
export type RegisteredDomain = {
  name: string;
  tls: DomainTLS;
  not_after?: string | null; // RFC 3339, certificate mode
  issuer?: string; // CN of the issuer, certificate mode
};

// Domain is a registration as the agent lists it, with the apps using it.
export type Domain = RegisteredDomain & {
  apps: string[] | null; // app ids using a covered host
};

// TLS_MODES words each mode: its badge label, what it means for a hostname
// (next to the badge in the app editor), and the setup it fits (the
// register dialog). Listed in the order the dialog offers them.
export const TLS_MODES: Record<DomainTLS, { label: string; summary: string; hint: string }> = {
  auto: {
    label: "Auto TLS",
    summary: "Free Let's Encrypt certificate",
    hint: "Gets a free Let's Encrypt certificate for each app hostname. For a DNS record pointing at this server (on Cloudflare: DNS only, grey cloud).",
  },
  certificate: {
    label: "Certificate",
    summary: "Your uploaded certificate",
    hint: "Uses a certificate you paste in, such as a Cloudflare Origin certificate behind the Cloudflare proxy.",
  },
  off: {
    label: "TLS off",
    summary: "Plain HTTP, for a tunnel or proxy in front",
    hint: "Serves plain HTTP, for when something in front handles HTTPS, such as a Cloudflare Tunnel.",
  },
};

// Mirrors Go's Ingress.Validate() (internal/domain/model/ingress.go): a
// strict lowercase RFC-1123 hostname, at most 253 characters.
const HOSTNAME_RE =
  /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$/;

export function validHostname(host: string): boolean {
  return host.length <= 253 && HOSTNAME_RE.test(host);
}

export function isWildcard(name: string): boolean {
  return name.startsWith("*.");
}

// domainNameError mirrors model.ValidateDomainName; "" means valid.
export function domainNameError(name: string): string {
  const host = isWildcard(name) ? name.slice(2) : name;
  if (name.length > 253 || !validHostname(host)) {
    return "Use a lowercase hostname like example.com, or *.example.com for every subdomain.";
  }
  return "";
}

const DAY_MS = 24 * 60 * 60 * 1000;

export type Expiry = {
  grade: "ok" | "soon" | "expired"; // soon = under 30 days
  days: number; // whole days left, rounded up (10 days minus a second is 10)
  day: string; // the date, YYYY-MM-DD
};

// expiry describes a certificate's not_after; null without a usable date.
export function expiry(notAfter: string | null | undefined, now: Date = new Date()): Expiry | null {
  if (!notAfter) return null;
  const left = new Date(notAfter).getTime() - now.getTime();
  if (Number.isNaN(left)) return null;
  return {
    grade: left <= 0 ? "expired" : left < 30 * DAY_MS ? "soon" : "ok",
    days: Math.ceil(left / DAY_MS),
    day: notAfter.slice(0, 10),
  };
}
