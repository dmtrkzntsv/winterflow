import { Plus, Trash2 } from "lucide-react";

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
import { HostnameField } from "@/components/hostname-field";
import { useRegistrations } from "@/hooks/use-registrations";
import type { RegisteredDomain } from "@/lib/domains";
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

const emptyIngress = (): AppIngress => ({ domains: [], redirects: [] });

// legacySSL is the stored "ssl" flag for a hostname under reg. The proxy
// ignores it (the registration decides HTTPS); it only picks the link
// scheme on app cards.
const legacySSL = (reg: RegisteredDomain) => reg.tls !== "off";

const REDIRECT_CODES = [301, 302, 307, 308] as const;

// IngressEditor edits an app's domains (host -> upstream port) and redirect
// rules. Hostnames must sit under a domain registered on the server; TLS
// follows that registration's mode (shown as a badge). It matches the
// Files/Variables card idiom in app-editor.tsx: local rows keyed by localId,
// patched via map/filter over the config array.
export function IngressEditor({ state, onChange, appId, serverId }: Props) {
  // The server's registrations, offered as suggestions for empty hostnames.
  const { registrations, refresh } = useRegistrations(serverId);

  const ing = state.config.ingress ?? emptyIngress();
  const setIngress = (next: AppIngress) =>
    onChange({ ...state, config: { ...state.config, ingress: next } });

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

  const removeDomain = (id: string) =>
    setIngress({ ...ing, domains: ing.domains.filter((d) => d.id !== id) });

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
    <EditorSection
      title="Domains & Routing"
      description="Serve the app on hostnames under the domains on this server. Each domain sets how HTTPS works."
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
          {ing.domains.map((d) => (
            <HostnameField
              key={d.id}
              rowId={d.id}
              value={d.domain}
              label="Hostname"
              placeholder="app.example.com"
              // Phones put the hostname on its own row: it's the long,
              // important part, and the port fits beside the button.
              rowClassName="flex flex-wrap items-center gap-2 sm:flex-nowrap"
              inputClassName="basis-full sm:basis-auto"
              checked
              serverId={serverId}
              appId={appId}
              registrations={registrations}
              onChange={(domain) => updateDomain(d.id, { domain })}
              onRegistration={(reg) => {
                if (d.ssl !== legacySSL(reg)) updateDomain(d.id, { ssl: legacySSL(reg) });
              }}
              onReturn={refresh}
            >
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
            </HostnameField>
          ))}
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
          {ing.redirects.map((r) => (
            <HostnameField
              key={r.id}
              rowId={r.id}
              value={r.domain}
              label="Redirect hostname"
              placeholder="old.example.com"
              rowClassName="flex flex-wrap items-center gap-2"
              inputClassName="sm:max-w-56"
              // Path rules ride a domain checked above; only a domain-level
              // redirect claims its hostname.
              checked={r.path === ""}
              serverId={serverId}
              appId={appId}
              registrations={registrations}
              onChange={(domain) => updateRedirect(r.id, { domain })}
              onRegistration={(reg) => {
                if (r.ssl !== legacySSL(reg)) updateRedirect(r.id, { ssl: legacySSL(reg) });
              }}
              onReturn={refresh}
            >
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
            </HostnameField>
          ))}
        </div>
      </div>
    </EditorSection>
  );
}
