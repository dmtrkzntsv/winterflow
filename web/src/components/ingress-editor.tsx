import { Plus, Trash2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Switch } from "@/components/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { apiBaseUrl } from "@/config";
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
};

const emptyIngress = (): AppIngress => ({ domains: [], redirects: [] });

const REDIRECT_CODES = [301, 302, 307, 308] as const;

// IngressEditor edits an app's domains (host -> upstream port, optional
// Let's-Encrypt HTTPS) and redirect rules. It matches the Files/Variables
// card idiom in app-editor.tsx: local rows keyed by localId, patched via
// map/filter over the config array.
export function IngressEditor({ state, onChange, appId }: Props) {
  // taken[domain] = inline error string ("" or absent = not flagged) from the
  // live availability check. Keyed by the domain text itself, so editing a
  // domain to a different string naturally stops showing the old error —
  // there's nothing extra to clear.
  const [taken, setTaken] = useState<Record<string, string>>({});
  const timers = useRef<Record<string, ReturnType<typeof setTimeout>>>({});

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
        const qs = new URLSearchParams({ domain });
        if (appId) qs.set("app_id", appId);
        const res = await fetch(`${apiBaseUrl}/api/v1/domains/check?${qs}`, {
          credentials: "include",
        });
        const body = (await res.json()) as {
          data?: {
            available?: boolean;
            claims?: { app_name: string; server_name: string }[];
          };
        };
        const claim = body.data?.claims?.[0];
        setTaken((prev) => ({
          ...prev,
          [domain]:
            body.data?.available === false && claim
              ? `Already used by app "${claim.app_name}" on server "${claim.server_name}".`
              : "",
        }));
      } catch {
        // Availability is advisory; the save-time check is authoritative.
      }
    }, 400);
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
      setTaken((prev) => {
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

  const removeRedirect = (id: string) =>
    setIngress({
      ...ing,
      redirects: ing.redirects.filter((r) => r.id !== id),
    });

  return (
    <Card>
      <CardHeader>
        <CardTitle>Domains & Routing</CardTitle>
      </CardHeader>
      <CardContent className="space-y-6">
        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <Label className="text-sm font-medium">Domains</Label>
            <Button size="sm" variant="outline" onClick={addDomain}>
              <Plus className="size-4" /> Add domain
            </Button>
          </div>
          {ing.domains.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              Add a domain to route external traffic to this app.
            </p>
          ) : null}
          {ing.domains.map((d) => (
            <div key={d.id} className="rounded-md border p-3">
              <div className="flex items-center gap-2">
                <Input
                  value={d.domain}
                  placeholder="app.example.com"
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
                  className="w-28 shrink-0"
                  onChange={(e) =>
                    updateDomain(d.id, {
                      upstream_port:
                        e.target.value === "" ? "" : Number(e.target.value),
                    })
                  }
                />
                <label className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
                  <Switch
                    checked={d.ssl}
                    onCheckedChange={(c) => updateDomain(d.id, { ssl: c })}
                  />
                  HTTPS via Let's Encrypt
                </label>
                <Button
                  size="icon"
                  variant="ghost"
                  className="size-8 shrink-0"
                  onClick={() => removeDomain(d.id)}
                >
                  <Trash2 className="size-4" />
                </Button>
              </div>
              {taken[d.domain] ? (
                <p className="mt-2 text-xs text-destructive">{taken[d.domain]}</p>
              ) : null}
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
            <p className="text-sm text-muted-foreground">
              Redirect a domain (or a path on it) to another URL.
            </p>
          ) : null}
          {ing.redirects.map((r) => (
            <div key={r.id} className="rounded-md border p-3">
              <div className="flex flex-wrap items-center gap-2">
                <Input
                  value={r.domain}
                  placeholder="old.example.com"
                  className="font-mono sm:max-w-56"
                  onChange={(e) =>
                    updateRedirect(r.id, {
                      domain: e.target.value.trim().toLowerCase(),
                    })
                  }
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
                {r.path === "" ? (
                  <label className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
                    <Switch
                      checked={r.ssl}
                      onCheckedChange={(c) => updateRedirect(r.id, { ssl: c })}
                    />
                    HTTPS via Let's Encrypt
                  </label>
                ) : null}
                <Button
                  size="icon"
                  variant="ghost"
                  className="size-8 shrink-0"
                  onClick={() => removeRedirect(r.id)}
                >
                  <Trash2 className="size-4" />
                </Button>
              </div>
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
  );
}
