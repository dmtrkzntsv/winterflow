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

// TLS_MODES words each mode: its badge label and summary (the app editor's
// hostname hints), its state on the Domains page, its choice in the
// register dialog with the setup it fits. Listed in the order the dialog
// offers them.
export const TLS_MODES: Record<
  DomainTLS,
  { label: string; summary: string; state: string; choice: string; hint: string }
> = {
  auto: {
    label: "Auto TLS",
    summary: "Free Let's Encrypt certificate",
    state: "Automatic HTTPS",
    choice: "Automatic (Let's Encrypt)",
    hint: "A free certificate for each app address. Point the domain's DNS at this server (on Cloudflare: DNS only, grey cloud).",
  },
  certificate: {
    label: "Certificate",
    summary: "Your uploaded certificate",
    state: "Own certificate",
    choice: "My own certificate",
    hint: "Paste a certificate and its private key, such as a Cloudflare Origin certificate behind the Cloudflare proxy.",
  },
  off: {
    label: "TLS off",
    summary: "Plain HTTP, for a tunnel or proxy in front",
    state: "HTTPS off",
    choice: "Off — behind Cloudflare Tunnel or another proxy",
    hint: "This server serves plain HTTP; the tunnel or proxy in front of it handles HTTPS.",
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

// coveringDomain is the registration that covers a hostname, as the agent
// picks it: an exact name wins over a wildcard, and "*.example.com" covers
// one label under example.com (a.example.com, not a.b.example.com nor
// example.com itself). undefined when none does.
export function coveringDomain(host: string, registrations: readonly RegisteredDomain[]): RegisteredDomain | undefined {
  const name = host.toLowerCase();
  const exact = registrations.find((r) => r.name === name);
  if (exact) return exact;
  const dot = name.indexOf(".");
  if (dot <= 0) return undefined;
  return registrations.find((r) => r.name === `*.${name.slice(dot + 1)}`);
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

// expiryNote is what the Domains page says about a certificate-mode
// domain's certificate: nothing while it has 30 days or more, a countdown
// under that, the date once it is past. Other modes renew themselves or
// have no certificate here.
export function expiryNote(
  d: Pick<RegisteredDomain, "tls" | "not_after">,
  now: Date = new Date(),
): { text: string; expired: boolean } | null {
  if (d.tls !== "certificate") return null;
  const left = expiry(d.not_after, now);
  if (!left || left.grade === "ok") return null;
  if (left.grade === "expired") return { text: `Expired ${left.day}`, expired: true };
  return { text: `Expires in ${left.days === 1 ? "1 day" : `${left.days} days`}`, expired: false };
}
