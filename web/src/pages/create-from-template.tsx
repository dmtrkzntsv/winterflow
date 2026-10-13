import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { EditorSection, StickyActionBar } from "@/components/editor-section";
import { ErrorText } from "@/components/error-text";
import { HostHints } from "@/components/host-hints";
import { NativeSelect } from "@/components/native-select";
import { EmptyNote, LoadError, PageSpinner } from "@/components/page-states";
import { ParameterFields } from "@/components/parameter-fields";
import { useApps } from "@/context/use-apps";
import { useBackups } from "@/context/use-backups";
import { useServerPath } from "@/context/use-server-path";
import { useServers } from "@/context/use-servers";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { validHostname } from "@/lib/domains";
import { useHostCheck } from "@/hooks/use-host-check";
import {
  expand,
  fieldErrors,
  newKey,
  plainValues,
  previewHosts,
  splitValues,
  type HostPreview,
} from "@/lib/template-params";
import { ApiError, sealSecrets, type TemplateSummary, type TemplateView, type UpdateInstanceBody } from "@/lib/templates";

const BREADCRUMBS = [{ label: "Apps", href: "/" }, { label: "From template" }];

// CreateFromTemplatePage is Add app → From template on a server: pick a
// template and revision, fill in its parameters (secrets encrypted in the
// browser for this server), pick storages for its backups, see the hosts
// the app will get, then add the app and open it.
export default function CreateFromTemplatePage() {
  useAppBreadcrumbs(BREADCRUMBS);
  const navigate = useNavigate();
  const toServer = useServerPath();
  const { activeServerId } = useServers();
  const { refresh } = useApps();
  const { serverStorages } = useBackups();
  const api = useTemplatesApi();

  const [templates, setTemplates] = useState<TemplateSummary[] | null>(null);
  const [templateId, setTemplateId] = useState("");
  const [revision, setRevision] = useState(0); // 0: the latest
  const [view, setView] = useState<TemplateView | null>(null);
  const [values, setValues] = useState<Record<string, string>>({});
  const [storages, setStorages] = useState<string[]>([]);
  // storageOverrides are the backup storages the user picked (else the
  // template's default).
  const [storageOverrides, setStorageOverrides] = useState<Record<string, string>>({});
  const [loadError, setLoadError] = useState("");
  // attempt counts Retry clicks: each one runs the reads again.
  const [attempt, setAttempt] = useState(0);
  // key names this app to the API: a retry after a lost answer replays the
  // same create instead of adding a second app.
  const [key] = useState(newKey);
  // claimed: the API accepted the create (the key is taken), so a retry
  // after an agent failure is an update of that pending instance.
  const [claimed, setClaimed] = useState(false);
  const [tried, setTried] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    let live = true;
    api.listTemplates().then(
      (list) => {
        if (!live) return;
        setTemplates(list);
        if (list.length > 0) setTemplateId((id) => id || list[0].id);
      },
      (e: unknown) => live && setLoadError(e instanceof Error ? e.message : "Failed to load templates"),
    );
    return () => {
      live = false;
    };
  }, [api, attempt]);

  useEffect(() => {
    serverStorages().then(setStorages, () => setStorages([]));
  }, [serverStorages]);

  useEffect(() => {
    if (!templateId) return;
    let live = true;
    api.getTemplate(templateId, revision || undefined).then(
      (v) => {
        if (!live) return;
        setView(v);
        setStorageOverrides({});
      },
      (e: unknown) => live && setLoadError(e instanceof Error ? e.message : "Failed to load the template"),
    );
    return () => {
      live = false;
    };
  }, [api, templateId, revision, attempt]);

  const params = view?.body.parameters ?? [];
  const plain = useMemo(() => plainValues(view?.body.parameters, values), [view, values]);
  const hosts = view ? previewHosts(view.body, plain) : [];
  const appName = view ? expand(view.body.app_name, plain) : "";
  const backups = Object.entries(view?.body.backups ?? {});

  // send creates the instance, or once the API holds this key (an earlier
  // attempt whose deploy failed: the instance is pending) updates it. A
  // refused create (a 4xx other than 409) claimed nothing; a 409 (the key is
  // held) or a 5xx (the claim came before the dispatch failed) may have.
  const send = async (body: UpdateInstanceBody, template: TemplateView) => {
    if (claimed) {
      try {
        await api.updateInstance(body);
        return;
      } catch (e) {
        if (!(e instanceof ApiError && e.status === 404)) throw e;
      }
    }
    setClaimed(true);
    try {
      await api.createInstance({ ...body, template_id: template.id, revision: template.revision });
    } catch (e) {
      if (e instanceof ApiError && e.status >= 400 && e.status < 500 && e.status !== 409) setClaimed(false);
      throw e;
    }
  };

  const create = async () => {
    if (!view || !activeServerId) return;
    setTried(true);
    if (Object.keys(fieldErrors(params, values)).length > 0) return;
    setBusy(true);
    setError("");
    try {
      const { plain, typed } = splitValues(params, values);
      const parameters = Object.fromEntries(Object.entries(plain).filter(([, v]) => v !== ""));
      const secrets = await sealSecrets(api, activeServerId, typed);
      const overrides: Record<string, string> = {};
      for (const [name, d] of backups) {
        const picked = storageOverrides[name];
        if (picked !== undefined && picked !== (d.storage ?? "")) overrides[name] = picked;
      }
      const body = {
        server_id: activeServerId,
        key,
        parameters,
        secrets,
        ...(Object.keys(overrides).length ? { backups: overrides } : {}),
      };
      await send(body, view);
      const [inst] = await Promise.all([api.getInstance(activeServerId, key), refresh()]);
      toast.success("App added");
      navigate(toServer(`/app/${inst.app_id}`));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't add the app");
    } finally {
      setBusy(false);
    }
  };

  if (loadError) {
    return (
      <LoadError
        className="mx-auto max-w-5xl text-center"
        message={loadError}
        onRetry={() => {
          setLoadError("");
          setAttempt((n) => n + 1);
        }}
      />
    );
  }
  if (templates === null || (templateId && !view)) return <PageSpinner />;
  if (templates.length === 0) {
    return (
      <EmptyNote className="mx-auto max-w-5xl p-6 text-center">
        The organization has no templates yet.{" "}
        <Link to="/org/templates" className="underline underline-offset-4">
          Add one
        </Link>{" "}
        or save an app as a template from its Settings.
      </EmptyNote>
    );
  }

  return (
    <div className="mx-auto w-full max-w-5xl">
      <StickyActionBar className="mb-6 gap-x-4">
        <div className="mr-auto min-w-0">
          <h1 className="text-xl font-semibold tracking-tight">Add app from template</h1>
          <p className="text-sm text-muted-foreground">The app keeps the template's files until you unlink it.</p>
        </div>
        <Button variant="ghost" onClick={() => navigate(toServer())} disabled={busy}>
          Cancel
        </Button>
        <Button onClick={() => void create()} disabled={busy || !view}>
          {busy ? "Adding…" : "Add app"}
        </Button>
      </StickyActionBar>

      {error ? <ErrorText alert icon text={error} className="mb-4" /> : null}

      <EditorSection title="Template" description="The latest revision unless you pick an older one.">
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="grid gap-2">
            <Label htmlFor="pick-template">Template</Label>
            <NativeSelect
              id="pick-template"
              value={templateId}
              disabled={claimed}
              onChange={(e) => {
                setTemplateId(e.target.value);
                setRevision(0);
                setView(null);
              }}
            >
              {templates.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name}
                </option>
              ))}
            </NativeSelect>
          </div>
          <div className="grid gap-2">
            <Label htmlFor="pick-revision">Revision</Label>
            <NativeSelect
              id="pick-revision"
              value={String(revision)}
              disabled={claimed}
              onChange={(e) => setRevision(Number(e.target.value))}
            >
              <option value="0">Latest (r{view?.latest_revision})</option>
              {(view?.revisions ?? []).map((r) => (
                <option key={r.revision} value={String(r.revision)}>
                  r{r.revision}
                  {r.note ? ` · ${r.note}` : ""}
                </option>
              ))}
            </NativeSelect>
          </div>
        </div>
      </EditorSection>

      <EditorSection title="Parameters" description="Required ones first. Secret values are encrypted in your browser for this server.">
        {params.length === 0 ? (
          <p className="text-sm text-muted-foreground">This template takes no values.</p>
        ) : (
          <ParameterFields
            params={params}
            values={values}
            onChange={(name, v) => setValues((prev) => ({ ...prev, [name]: v }))}
            showRequired={tried}
          />
        )}
      </EditorSection>

      {backups.length > 0 ? (
        <EditorSection title="Backups" description="Where each backup the template turns on is stored on this server.">
          <div className="grid gap-3">
            {backups.map(([name, d]) => {
              const value = storageOverrides[name] ?? d.storage ?? "";
              const missing = value !== "" && !storages.includes(value);
              return (
                <div key={name} className="grid gap-1.5">
                  <Label htmlFor={`storage-${name}`}>
                    <code className="font-mono">{name}</code>
                    {d.enabled ? null : <span className="text-xs font-normal text-muted-foreground">off by default</span>}
                  </Label>
                  <NativeSelect
                    id={`storage-${name}`}
                    aria-label={`Storage for ${name}`}
                    value={value}
                    onChange={(e) => setStorageOverrides((prev) => ({ ...prev, [name]: e.target.value }))}
                  >
                    {(d.storage ?? "") === "" ? <option value="">No storage</option> : null}
                    {missing ? <option value={value}>{value} (not on this server)</option> : null}
                    {storages.map((s) => (
                      <option key={s} value={s}>
                        {s}
                      </option>
                    ))}
                  </NativeSelect>
                  {missing ? (
                    <p className="text-xs text-destructive">
                      This server has no storage named {value}.{" "}
                      <Link to={toServer("/settings?tab=storages")} className="underline underline-offset-4">
                        Add it
                      </Link>{" "}
                      or pick another.
                    </p>
                  ) : null}
                </div>
              );
            })}
          </div>
        </EditorSection>
      ) : null}

      <EditorSection title="The app" description="What the app will be called and the hosts it will answer on.">
        <p className="text-sm">
          <span className="text-muted-foreground">Name: </span>
          <span className="font-medium">{appName.trim() || "fill in the parameters"}</span>
        </p>
        {hosts.length === 0 ? (
          <p className="text-sm text-muted-foreground">No hosts.</p>
        ) : (
          <ul aria-label="Hosts" className="grid gap-2">
            {hosts.map((h, i) => (
              <HostPreviewRow key={i} serverId={activeServerId ?? ""} preview={h} />
            ))}
          </ul>
        )}
      </EditorSection>
    </div>
  );
}

// HostPreviewRow is one host the app will get, with the live check
// (another app's host, or no registered domain covering it) once its
// parameters are filled in.
function HostPreviewRow({ serverId, preview }: { serverId: string; preview: HostPreview }) {
  const ready = preview.missing.length === 0 && validHostname(preview.host);
  const check = useHostCheck(serverId, undefined, preview.host, { enabled: ready && !!serverId, delay: 400 });
  return (
    <li className="rounded-md border p-3 text-sm">
      <span className="font-mono">{preview.host || "…"}</span>
      {preview.port ? <span className="text-muted-foreground"> → port {preview.port}</span> : null}
      {preview.missing.length > 0 ? (
        <p className="mt-1 text-xs text-muted-foreground">Fill in {preview.missing.join(", ")}.</p>
      ) : !validHostname(preview.host) ? (
        <p className="mt-1 text-xs text-destructive">Not a valid hostname.</p>
      ) : check ? (
        <HostHints id={`host-${preview.host}`} check={check} />
      ) : null}
    </li>
  );
}
