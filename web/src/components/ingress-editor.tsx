import { ExternalLink, Plus, Trash2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { EditorSection } from "@/components/editor-section";
import { TLSBadge } from "@/components/tls-badge";
import { apiBaseUrl } from "@/config";
import { useDocker } from "@/context/use-docker";
import { cn } from "@/lib/utils";
import {
  isWildcard,
  TLS_SUMMARY,
  winningDomain,
  type RegisteredDomain,
} from "@/lib/domains";
import {
  localId,
  type AppEditorState,
  type AppIngress,
  type IngressDomain,
  type IngressRedirect,
} from "@/types/app-config";

type Props = {
  state: AppEditorState;
  onChange: (next: AppEditorState) => void;
  appId?: string;
  serverId: string;
};

// Issue is the live check's verdict on one hostname: another app on this
// server claims it, or no registered domain covers it.
type Issue = { kind: "taken"; app: string } | { kind: "uncovered" };

// HostHints shows, under a hostname input, the live check's problem (with a
// way to fix it) or what HTTPS the covering registration gives the host. id
// lets the input point at it (aria-describedby).
function HostHints({
  id,
  host,
  issue,
  registrations,
}: {
  id: string;
  host: string;
  issue?: Issue;
  registrations: RegisteredDomain[];
}) {
  if (!host) return null;
  if (issue?.kind === "taken") {
    return (
      <p id={id} className="mt-2 text-xs text-destructive">
        Already used by <span className="font-medium">{issue.app}</span> on this server.
      </p>
    );
  }
  if (issue?.kind === "uncovered") {
    // A new tab: following the link here would throw away the unsaved form.
    // The editor re-checks when the user comes back.
    return (
      <p id={id} className="mt-2 text-xs text-destructive">
        No registered domain covers this hostname.{" "}
        <Link
          to="/settings?tab=domains"
          target="_blank"
          rel="noreferrer"
          className="inline-flex items-center gap-0.5 font-medium underline underline-offset-4"
        >
          Register one
          <ExternalLink className="size-3" aria-hidden />
          <span className="sr-only">(opens in a new tab)</span>
        </Link>
      </p>
    );
  }
  const win = winningDomain(
    registrations.map((r) => r.name),
    host,
  );
  const reg = registrations.find((r) => r.name === win);
  if (!reg) return null;
  return (
    <div id={id} className="mt-2 flex flex-wrap items-center gap-x-1.5 gap-y-1 text-xs text-muted-foreground">
      <TLSBadge tls={reg.tls} />
      <span>
        {TLS_SUMMARY[reg.tls]} · set by{" "}
        <code className="font-mono [overflow-wrap:anywhere]">{reg.name}</code>
      </span>
    </div>
  );
}

// Suggestions offers the server's registrations for an empty hostname:
// an exact name fills the field; a wildcard fills ".suffix" and puts the
// caret before it, ready for the label.
function Suggestions({
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

const emptyIngress = (): AppIngress => ({ domains: [], redirects: [] });

const REDIRECT_CODES = [301, 302, 307, 308] as const;

// IngressEditor edits an app's domains (host -> upstream port) and redirect
// rules. Hostnames must sit under a domain registered on the server; TLS
// follows that registration's mode (shown as a badge). It matches the
// Files/Variables card idiom in app-editor.tsx: local rows keyed by localId,
// patched via map/filter over the config array.
export function IngressEditor({ state, onChange, appId, serverId }: Props) {
  // issues[domain] = the live check's verdict (absent = fine or unchecked).
  // Keyed by the domain text itself, so editing a domain to a different
  // string naturally stops showing the old error — nothing extra to clear.
  const [issues, setIssues] = useState<Record<string, Issue | undefined>>({});
  const timers = useRef<Record<string, ReturnType<typeof setTimeout>>>({});
  const inputs = useRef<Record<string, HTMLInputElement | null>>({});
  const [registrations, setRegistrations] = useState<RegisteredDomain[]>([]);
  // Bumped to reload the registrations (see the focus handler below).
  const [reloads, setReloads] = useState(0);
  const { listDomains } = useDocker();

  // The server's registrations drive suggestions and the TLS badges.
  useEffect(() => {
    let live = true;
    listDomains()
      .then((list) => live && setRegistrations(list))
      .catch(() => {
        // Advisory only: without them the editor just offers no suggestions.
      });
    return () => {
      live = false;
    };
  }, [listDomains, serverId, reloads]);

  // Cancel any in-flight debounce timers if the editor unmounts (e.g. the
  // user navigates away mid-check) so a stale check never calls setState.
  useEffect(() => {
    const timersAtMount = timers.current;
    return () => {
      Object.values(timersAtMount).forEach(clearTimeout);
    };
  }, []);

  const ing = state.config.ingress ?? emptyIngress();
  const setIngress = (next: AppIngress) =>
    onChange({ ...state, config: { ...state.config, ingress: next } });

  const checkDomain = (id: string, domain: string) => {
    clearTimeout(timers.current[id]);
    if (!domain) return;
    timers.current[id] = setTimeout(async () => {
      try {
        const qs = new URLSearchParams({ server_id: serverId, domain });
        if (appId) qs.set("app_id", appId);
        const res = await fetch(`${apiBaseUrl}/api/v1/domains/check?${qs}`, {
          credentials: "include",
        });
        if (!res.ok) return; // e.g. not a hostname yet: the form validation says so
        const body = (await res.json()) as {
          data?: {
            available?: boolean;
            covered?: boolean;
            claims?: { app_name: string }[] | null;
          };
        };
        const claim = body.data?.claims?.[0];
        const issue: Issue | undefined =
          body.data?.available === false && claim
            ? { kind: "taken", app: claim.app_name }
            : body.data?.covered === false
              ? { kind: "uncovered" }
              : undefined;
        setIssues((prev) => ({ ...prev, [domain]: issue }));
      } catch {
        // Advisory; the save-time check is authoritative.
      }
    }, 400);
  };

  // pick fills a hostname from a suggestion; a wildcard's ".suffix" gets
  // the caret at its start so the label can be typed straight away.
  const pick = (
    id: string,
    set: (value: string) => void,
    value: string,
    caretAtStart: boolean,
  ) => {
    set(value);
    if (!caretAtStart) checkDomain(id, value);
    requestAnimationFrame(() => {
      const el = inputs.current[id];
      if (!el) return;
      el.focus();
      if (caretAtStart) el.setSelectionRange(0, 0);
    });
  };

  // An "uncovered" hint links to the Domains tab in a new tab. When the user
  // comes back, reload the registrations and re-check the flagged hostnames
  // so a domain registered meanwhile clears the hint. Only while something
  // is flagged: a plain focus change costs nothing.
  const uncovered = [...ing.domains, ...ing.redirects.filter((r) => r.path === "")].filter(
    (row) => issues[row.domain]?.kind === "uncovered",
  );
  useEffect(() => {
    if (uncovered.length === 0) return;
    const onFocus = () => {
      setReloads((n) => n + 1);
      uncovered.forEach((row) => checkDomain(row.id, row.domain));
    };
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  });

  // --- domains ---
  const updateDomain = (id: string, patch: Partial<IngressDomain>) =>
    setIngress({
      ...ing,
      domains: ing.domains.map((d) => (d.id === id ? { ...d, ...patch } : d)),
    });

  const addDomain = () =>
    setIngress({
      ...ing,
      domains: [
        ...ing.domains,
        { id: localId("dom"), domain: "", upstream_port: "", ssl: true },
      ],
    });

  const removeDomain = (id: string) => {
    clearTimeout(timers.current[id]);
    delete timers.current[id];
    const removed = ing.domains.find((d) => d.id === id);
    if (removed) {
      setIssues((prev) => {
        if (!(removed.domain in prev)) return prev;
        const next = { ...prev };
        delete next[removed.domain];
        return next;
      });
    }
    setIngress({ ...ing, domains: ing.domains.filter((d) => d.id !== id) });
  };

  // --- redirects ---
  const updateRedirect = (id: string, patch: Partial<IngressRedirect>) =>
    setIngress({
      ...ing,
      redirects: ing.redirects.map((r) =>
        r.id === id ? { ...r, ...patch } : r,
      ),
    });

  const addRedirect = () =>
    setIngress({
      ...ing,
      redirects: [
        ...ing.redirects,
        {
          id: localId("red"),
          domain: "",
          path: "",
          to: "",
          code: 301,
          ssl: true,
        },
      ],
    });

  const removeRedirect = (id: string) => {
    clearTimeout(timers.current[id]);
    delete timers.current[id];
    setIngress({
      ...ing,
      redirects: ing.redirects.filter((r) => r.id !== id),
    });
  };

  return (
    <EditorSection
      title="Domains & Routing"
      description="Serve the app on hostnames under the domains registered on this server. Each domain sets how HTTPS works."
    >
      <div className="space-y-6">
        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <Label className="text-sm font-medium">Hostnames</Label>
            <Button size="sm" variant="outline" onClick={addDomain}>
              <Plus className="size-4" /> Add hostname
            </Button>
          </div>
          {ing.domains.length === 0 ? (
            <p className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">
              Add a hostname to send web traffic to this app.
            </p>
          ) : null}
          {ing.domains.map((d) => {
            const issue = issues[d.domain];
            const hintId = `${d.id}-hint`;
            return (
              <div key={d.id} className="rounded-md border p-3">
                {/* Phones put the hostname on its own row: it's the long,
                    important part, and the port fits beside the button. */}
                <div className="flex flex-wrap items-center gap-2 sm:flex-nowrap">
                  <Input
                    ref={(el) => {
                      inputs.current[d.id] = el;
                    }}
                    value={d.domain}
                    placeholder="app.example.com"
                    aria-label="Hostname"
                    aria-invalid={issue !== undefined}
                    aria-describedby={d.domain ? hintId : undefined}
                    className={cn(
                      "basis-full font-mono sm:basis-auto",
                      issue && "border-destructive focus-visible:ring-destructive",
                    )}
                    onChange={(e) => {
                      const domain = e.target.value.trim().toLowerCase();
                      updateDomain(d.id, { domain });
                      checkDomain(d.id, domain);
                    }}
                  />
                  <Input
                    value={d.upstream_port}
                    type="number"
                    placeholder="8088"
                    aria-label="Upstream port"
                    title="Upstream port"
                    className="w-28 shrink-0"
                    onChange={(e) =>
                      updateDomain(d.id, {
                        upstream_port:
                          e.target.value === "" ? "" : Number(e.target.value),
                      })
                    }
                  />
                  <Button
                    size="icon"
                    variant="ghost"
                    className="ml-auto size-8 shrink-0 text-muted-foreground hover:text-destructive sm:ml-0"
                    aria-label={`Remove ${d.domain || "hostname"}`}
                    onClick={() => removeDomain(d.id)}
                  >
                    <Trash2 className="size-4" />
                  </Button>
                </div>
                {d.domain === "" ? (
                  <Suggestions
                    registrations={registrations}
                    onPick={(value, caret) =>
                      pick(d.id, (domain) => updateDomain(d.id, { domain }), value, caret)
                    }
                  />
                ) : (
                  <HostHints id={hintId} host={d.domain} issue={issue} registrations={registrations} />
                )}
              </div>
            );
          })}
          {ing.domains.length > 0 ? (
            <p className="text-xs text-muted-foreground">
              The compose file must publish each port on 127.0.0.1, e.g.{" "}
              <code>127.0.0.1:8088:80</code>.
            </p>
          ) : null}
        </div>

        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <Label className="text-sm font-medium">Redirects</Label>
            <Button size="sm" variant="outline" onClick={addRedirect}>
              <Plus className="size-4" /> Add redirect
            </Button>
          </div>
          {ing.redirects.length === 0 ? (
            <p className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">
              Redirect a hostname (or a path on it) to another URL.
            </p>
          ) : null}
          {ing.redirects.map((r) => {
            const issue = r.path === "" ? issues[r.domain] : undefined;
            const hintId = `${r.id}-hint`;
            return (
              <div key={r.id} className="rounded-md border p-3">
                <div className="flex flex-wrap items-center gap-2">
                  <Input
                    ref={(el) => {
                      inputs.current[r.id] = el;
                    }}
                    value={r.domain}
                    placeholder="old.example.com"
                    aria-label="Redirect hostname"
                    aria-invalid={issue !== undefined}
                    aria-describedby={r.path === "" && r.domain ? hintId : undefined}
                    className={cn(
                      "font-mono sm:max-w-56",
                      issue && "border-destructive focus-visible:ring-destructive",
                    )}
                    onChange={(e) => {
                      const domain = e.target.value.trim().toLowerCase();
                      updateRedirect(r.id, { domain });
                      // Path rules ride a domain checked above; only a
                      // domain-level redirect claims its hostname.
                      if (r.path === "") checkDomain(r.id, domain);
                    }}
                  />
                  <Input
                    value={r.path}
                    placeholder="/old-path/* (empty = whole domain)"
                    aria-label="Redirect path"
                    className="font-mono sm:max-w-64"
                    onChange={(e) =>
                      updateRedirect(r.id, { path: e.target.value })
                    }
                  />
                  <Input
                    value={r.to}
                    placeholder="https://new.example.com/new-path"
                    aria-label="Redirect to"
                    className="font-mono flex-1"
                    onChange={(e) => updateRedirect(r.id, { to: e.target.value })}
                  />
                  <Select
                    value={String(r.code)}
                    onValueChange={(v) =>
                      updateRedirect(r.id, {
                        code: Number(v) as IngressRedirect["code"],
                      })
                    }
                  >
                    <SelectTrigger className="h-9 w-24 shrink-0">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {REDIRECT_CODES.map((code) => (
                        <SelectItem key={code} value={String(code)}>
                          {code}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <Button
                    size="icon"
                    variant="ghost"
                    className="size-8 shrink-0 text-muted-foreground hover:text-destructive"
                    aria-label="Remove redirect"
                    onClick={() => removeRedirect(r.id)}
                  >
                    <Trash2 className="size-4" />
                  </Button>
                </div>
                {r.path !== "" ? null : r.domain === "" ? (
                  <Suggestions
                    registrations={registrations}
                    onPick={(value, caret) =>
                      pick(r.id, (domain) => updateRedirect(r.id, { domain }), value, caret)
                    }
                  />
                ) : (
                  <HostHints id={hintId} host={r.domain} issue={issue} registrations={registrations} />
                )}
              </div>
            );
          })}
        </div>
      </div>
    </EditorSection>
  );
}
