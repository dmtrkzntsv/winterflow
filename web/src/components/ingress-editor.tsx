import { Plus, Trash2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";

import { Badge } from "@/components/ui/badge";
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
import { apiBaseUrl } from "@/config";
import { useDocker } from "@/context/use-docker";
import {
  isWildcard,
  TLS_LABELS,
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
// way to fix it) or the TLS mode the covering registration gives the host.
function HostHints({
  host,
  issue,
  registrations,
}: {
  host: string;
  issue?: Issue;
  registrations: RegisteredDomain[];
}) {
  if (!host) return null;
  if (issue?.kind === "taken") {
    return (
      <p className="mt-2 text-xs text-destructive">
        Taken by app "{issue.app}" on this server.
      </p>
    );
  }
  if (issue?.kind === "uncovered") {
    return (
      <p className="mt-2 text-xs text-destructive">
        Not covered by a domain registered on this server.{" "}
        <Link to="/settings?tab=domains" className="font-medium underline underline-offset-4">
          Register a domain
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
    <p className="mt-2 flex items-center gap-1.5 text-xs text-muted-foreground">
      <Badge variant={reg.tls === "off" ? "outline" : "secondary"}>{TLS_LABELS[reg.tls]}</Badge>
      via <code className="font-mono">{reg.name}</code>
    </p>
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
      <span>Use:</span>
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
  }, [listDomains, serverId]);

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
      description="Serve the app through the built-in proxy on hostnames under the domains registered on this server. TLS follows each domain's mode."
    >
      <div className="space-y-6">
        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <Label className="text-sm font-medium">Domains</Label>
            <Button size="sm" variant="outline" onClick={addDomain}>
              <Plus className="size-4" /> Add domain
            </Button>
          </div>
          {ing.domains.length === 0 ? (
            <p className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">
              Add a domain to route external traffic to this app.
            </p>
          ) : null}
          {ing.domains.map((d) => (
            <div key={d.id} className="rounded-md border p-3">
              <div className="flex items-center gap-2">
                <Input
                  ref={(el) => {
                    inputs.current[d.id] = el;
                  }}
                  value={d.domain}
                  placeholder="app.example.com"
                  aria-label="Hostname"
                  className="font-mono"
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
                  className="size-8 shrink-0"
                  aria-label="Remove domain"
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
                <HostHints host={d.domain} issue={issues[d.domain]} registrations={registrations} />
              )}
              <p className="mt-2 text-xs text-muted-foreground">
                The compose file must publish this port on 127.0.0.1, e.g.{" "}
                <code>127.0.0.1:8088:80</code>.
              </p>
            </div>
          ))}
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
              Redirect a domain (or a path on it) to another URL.
            </p>
          ) : null}
          {ing.redirects.map((r) => (
            <div key={r.id} className="rounded-md border p-3">
              <div className="flex flex-wrap items-center gap-2">
                <Input
                  ref={(el) => {
                    inputs.current[r.id] = el;
                  }}
                  value={r.domain}
                  placeholder="old.example.com"
                  aria-label="Redirect hostname"
                  className="font-mono sm:max-w-56"
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
                  className="font-mono sm:max-w-64"
                  onChange={(e) =>
                    updateRedirect(r.id, { path: e.target.value })
                  }
                />
                <Input
                  value={r.to}
                  placeholder="https://new.example.com/new-path"
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
                  className="size-8 shrink-0"
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
                <HostHints host={r.domain} issue={issues[r.domain]} registrations={registrations} />
              )}
            </div>
          ))}
        </div>
      </div>
    </EditorSection>
  );
}
