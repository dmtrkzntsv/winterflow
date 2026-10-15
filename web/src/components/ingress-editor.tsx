import { Plus, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { FieldMessage } from "@/components/form-field";
import { NativeSelect } from "@/components/native-select";
import { HostnameField } from "@/components/hostname-field";
import { EmptyNote } from "@/components/page-states";
import { newAddressRow } from "@/lib/app-editor-io";
import type { RegisteredDomain } from "@/lib/domains";
import { invalidAttrs, type FieldErrors } from "@/lib/field-errors";
import {
  exposeTarget,
  localId,
  type AppEditorState,
  type AppIngress,
  type IngressDomain,
  type IngressRedirect,
} from "@/types/app-config";

export type IngressRowsProps = {
  state: AppEditorState;
  onChange: (next: AppEditorState) => void;
  appId?: string;
  serverId: string;
  // registrations are the server's domains (useRegistrations), offered as
  // suggestions for an empty hostname; onRefresh reads them again.
  registrations: RegisteredDomain[];
  onRefresh: () => void;
  // errors holds domain:/port:/redirect-*: messages (editorErrors).
  errors?: FieldErrors;
  addLabel?: string;
  // emptyNote stands in for no rows (null says nothing).
  emptyNote?: string | null;
};

const emptyIngress = (): AppIngress => ({ domains: [], redirects: [] });

// legacySSL is the stored "ssl" flag for a hostname under reg. The proxy
// ignores it (the registration decides HTTPS); it only picks the link
// scheme on app cards.
const legacySSL = (reg: RegisteredDomain) => reg.tls !== "off";

const REDIRECT_CODES = [301, 302, 307, 308] as const;

function ingressOf(state: AppEditorState): AppIngress {
  return state.config.ingress ?? emptyIngress();
}

// AddressRows edits where the app answers: hostname -> the port the compose
// file publishes on 127.0.0.1. Hostnames must sit under a domain registered
// on the server; HTTPS follows that registration (shown under the row).
export function AddressRows({
  state,
  onChange,
  appId,
  serverId,
  registrations,
  onRefresh,
  errors = {},
  addLabel = "Add hostname",
  emptyNote = "Add a hostname to send web traffic to this app.",
}: IngressRowsProps) {
  const ing = ingressOf(state);
  const setIngress = (next: AppIngress) => onChange({ ...state, config: { ...state.config, ingress: next } });
  const update = (id: string, patch: Partial<IngressDomain>) =>
    setIngress({ ...ing, domains: ing.domains.map((d) => (d.id === id ? { ...d, ...patch } : d)) });
  // The app's web ports (http expose entries) a hostname can be sent to.
  const targets = (state.expose ?? []).filter((e) => e.protocol === "http").map(exposeTarget);

  return (
    <div className="space-y-3">
      {ing.domains.length === 0 && emptyNote ? <EmptyNote>{emptyNote}</EmptyNote> : null}
      {ing.domains.map((d) => {
        const portId = `port-${d.id}`;
        const portError = errors[`port:${d.id}`];
        return (
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
            error={errors[`domain:${d.id}`]}
            footer={<FieldMessage id={portId} message={portError} className="mt-2" />}
            onChange={(domain) => update(d.id, { domain })}
            onRegistration={(reg) => {
              if (d.ssl !== legacySSL(reg)) update(d.id, { ssl: legacySSL(reg) });
            }}
            onReturn={onRefresh}
          >
            {targets.length > 0 || d.expose ? (
              <NativeSelect
                // The row's port field while it sends to an expose entry
                // (the Port input holds the id otherwise).
                id={d.expose ? portId : undefined}
                aria-label="Send to"
                value={d.expose ?? ""}
                {...invalidAttrs(portId, d.expose ? portError : undefined, "w-40 shrink-0 max-sm:h-10")}
                onChange={(e) => update(d.id, e.target.value ? { expose: e.target.value, upstream_port: "" } : { expose: undefined })}
              >
                {targets.map((t) => (
                  <option key={t} value={t}>
                    {t}
                  </option>
                ))}
                {d.expose && !targets.includes(d.expose) ? <option value={d.expose}>{d.expose}</option> : null}
                <option value="">Port…</option>
              </NativeSelect>
            ) : null}
            {d.expose ? null : (
              <Input
                id={portId}
                value={d.upstream_port}
                type="number"
                inputMode="numeric"
                placeholder="8088"
                aria-label="Port"
                title="The port the compose file publishes on 127.0.0.1"
                {...invalidAttrs(portId, portError, "w-28 shrink-0")}
                onChange={(e) => update(d.id, { upstream_port: e.target.value === "" ? "" : Number(e.target.value) })}
              />
            )}
            <Button
              size="icon"
              variant="ghost"
              className="ml-auto size-8 shrink-0 text-muted-foreground hover:text-destructive sm:ml-0"
              aria-label={`Remove ${d.domain || "hostname"}`}
              onClick={() => setIngress({ ...ing, domains: ing.domains.filter((x) => x.id !== d.id) })}
            >
              <Trash2 className="size-4" />
            </Button>
          </HostnameField>
        );
      })}
      {ing.domains.some((d) => !d.expose) ? (
        <p className="text-xs text-muted-foreground">
          The compose file must publish each port on 127.0.0.1, e.g. <code>127.0.0.1:8088:80</code>.
        </p>
      ) : null}
      <Button
        size="sm"
        variant="outline"
        onClick={() => setIngress({ ...ing, domains: [...ing.domains, { ...newAddressRow(), ...(targets[0] ? { expose: targets[0] } : {}) }] })}
      >
        <Plus className="size-4" /> {addLabel}
      </Button>
    </div>
  );
}

// RedirectRows edits redirect rules: a hostname (a whole-domain redirect
// claims it), or a path on a hostname claimed above, sent to another URL.
export function RedirectRows({
  state,
  onChange,
  appId,
  serverId,
  registrations,
  onRefresh,
  errors = {},
  addLabel = "Add redirect",
  emptyNote = "Redirect a hostname (or a path on it) to another URL.",
}: IngressRowsProps) {
  const ing = ingressOf(state);
  const setIngress = (next: AppIngress) => onChange({ ...state, config: { ...state.config, ingress: next } });
  const update = (id: string, patch: Partial<IngressRedirect>) =>
    setIngress({ ...ing, redirects: ing.redirects.map((r) => (r.id === id ? { ...r, ...patch } : r)) });
  const add = () =>
    setIngress({
      ...ing,
      redirects: [...ing.redirects, { id: localId("red"), domain: "", path: "", to: "", code: 301, ssl: true }],
    });

  return (
    <div className="space-y-3">
      {ing.redirects.length === 0 && emptyNote ? <EmptyNote>{emptyNote}</EmptyNote> : null}
      {ing.redirects.map((r) => {
        const pathId = `redirect-path-${r.id}`;
        const toId = `redirect-to-${r.id}`;
        const pathError = errors[`redirect-path:${r.id}`];
        const toError = errors[`redirect-to:${r.id}`];
        return (
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
            error={errors[`redirect-host:${r.id}`]}
            footer={
              <>
                <FieldMessage id={pathId} message={pathError} className="mt-2" />
                <FieldMessage id={toId} message={toError} className="mt-2" />
              </>
            }
            onChange={(domain) => update(r.id, { domain })}
            onRegistration={(reg) => {
              if (r.ssl !== legacySSL(reg)) update(r.id, { ssl: legacySSL(reg) });
            }}
            onReturn={onRefresh}
          >
            <Input
              id={pathId}
              value={r.path}
              placeholder="/old-path/* (empty = whole domain)"
              aria-label="Redirect path"
              {...invalidAttrs(pathId, pathError, "font-mono sm:max-w-64")}
              onChange={(e) => update(r.id, { path: e.target.value })}
            />
            <Input
              id={toId}
              value={r.to}
              placeholder="https://new.example.com/new-path"
              aria-label="Redirect to"
              {...invalidAttrs(toId, toError, "flex-1 font-mono")}
              onChange={(e) => update(r.id, { to: e.target.value })}
            />
            <Select value={String(r.code)} onValueChange={(v) => update(r.id, { code: Number(v) as IngressRedirect["code"] })}>
              <SelectTrigger className="h-9 w-24 shrink-0" aria-label="Redirect code">
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
              onClick={() => setIngress({ ...ing, redirects: ing.redirects.filter((x) => x.id !== r.id) })}
            >
              <Trash2 className="size-4" />
            </Button>
          </HostnameField>
        );
      })}
      <Button size="sm" variant="outline" onClick={add}>
        <Plus className="size-4" /> {addLabel}
      </Button>
    </div>
  );
}
