import { ExternalLink } from "lucide-react";
import { Link } from "react-router-dom";

import { Button } from "@/components/ui/button";
import { TLSBadge } from "@/components/tls-badge";
import { isWildcard, TLS_MODES, type RegisteredDomain } from "@/lib/domains";

// Issue is the live check's verdict on one hostname: another app on this
// server claims it, or no registered domain covers it.
export type Issue = { kind: "taken"; app: string } | { kind: "uncovered" };

// HostCheck is what the live check (GET /api/v1/domains/check) says about a
// hostname: its problem, if any, and the registration that covers it.
export type HostCheck = { issue?: Issue; registration?: RegisteredDomain };

// HostHints shows, under a hostname input, the live check's problem (with a
// way to fix it) or what HTTPS the covering registration gives the host. id
// lets the input point at it (aria-describedby).
export function HostHints({ id, check }: { id: string; check: HostCheck }) {
  if (check.issue?.kind === "taken") {
    return (
      <p id={id} className="mt-2 text-xs text-destructive">
        Already used by <span className="font-medium">{check.issue.app}</span> on this server.
      </p>
    );
  }
  if (check.issue?.kind === "uncovered") {
    // A new tab: following the link here would throw away the unsaved form.
    // The editor re-checks when the user comes back.
    return (
      <p id={id} className="mt-2 text-xs text-destructive">
        No domain on this server covers this hostname.{" "}
        <Link
          to="/settings?tab=domains"
          target="_blank"
          rel="noreferrer"
          className="inline-flex items-center gap-0.5 font-medium underline underline-offset-4"
        >
          Add it
          <ExternalLink className="size-3" aria-hidden />
          <span className="sr-only">(opens in a new tab)</span>
        </Link>
      </p>
    );
  }
  const reg = check.registration;
  if (!reg) return null;
  return (
    <div id={id} className="mt-2 flex flex-wrap items-center gap-x-1.5 gap-y-1 text-xs text-muted-foreground">
      <TLSBadge tls={reg.tls} />
      <span>
        {TLS_MODES[reg.tls].summary} · set by{" "}
        <code className="font-mono [overflow-wrap:anywhere]">{reg.name}</code>
      </span>
    </div>
  );
}

// Suggestions offers the server's registrations for an empty hostname:
// an exact name fills the field; a wildcard fills ".suffix" and puts the
// caret before it, ready for the label.
export function Suggestions({
  registrations,
  onPick,
}: {
  registrations: RegisteredDomain[];
  onPick: (value: string, caretAtStart: boolean) => void;
}) {
  if (registrations.length === 0) return null;
  return (
    <div className="mt-2 flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
      <span>Start from:</span>
      {registrations.map((r) => (
        <Button
          key={r.name}
          type="button"
          size="sm"
          variant="outline"
          className="h-6 px-2 font-mono text-xs"
          onClick={() =>
            isWildcard(r.name) ? onPick(r.name.slice(1), true) : onPick(r.name, false)
          }
        >
          {r.name}
        </Button>
      ))}
    </div>
  );
}
