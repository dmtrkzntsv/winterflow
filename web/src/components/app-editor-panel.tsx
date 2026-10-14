import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { AppEditor } from "@/components/app-editor";
import { ConfigBar } from "@/components/config-bar";
import { LoadError, PageSpinner } from "@/components/page-states";
import { useApps } from "@/context/use-apps";
import { useServerPath } from "@/context/use-server-path";
import { useServers } from "@/context/use-servers";
import {
  buildSavePayload,
  editorErrors,
  emptyEditorState,
  stateFromDetail,
} from "@/lib/app-editor-io";
import { readAppDetail } from "@/hooks/use-app-detail";
import { focusFirstInvalid, type FieldErrors } from "@/lib/field-errors";
import { appSub } from "@/lib/routes";
import { isManagedByTemplate } from "@/lib/templates";
import type { AppEditorState } from "@/types/app-config";

const NO_ERRORS: FieldErrors = {};

// panelErrors are editorErrors' problems the Configuration tab can show.
// Without the built-in proxy, or until the server is known, the Domains
// section is not shown, so a stored address or redirect (saved as it was
// loaded) can't block Save invisibly.
function panelErrors(state: AppEditorState, domainsShown: boolean): FieldErrors {
  const found = editorErrors(state);
  if (domainsShown) return found;
  return Object.fromEntries(
    Object.entries(found).filter(([k]) => !/^(domain:|port:|redirect-)/.test(k)),
  );
}

// AppEditorPanel is the Configuration tab of a plain app: it loads the
// app's current version, lets the user edit what runs, and saves in place
// (app.save with the app id → new version + redeploy). A refused save
// marks each bad field with its message. onOpenHistory opens the page's
// History from the action bar. reloadSignal changes when History rolled the
// app back or deployed its draft, or an update pulled its source: the
// editor then reads the app again, or, when it holds unsaved edits, says so
// and blocks saving until Reload (a save from the old tree would silently
// undo the change). onDirtyChange hears whether it holds unsaved edits (the
// page asks before leaving them), and false when it goes.
export function AppEditorPanel({
  appId,
  onOpenHistory,
  reloadSignal = 0,
  onDirtyChange,
}: {
  appId: string;
  onOpenHistory?: () => void;
  reloadSignal?: number;
  onDirtyChange?: (dirty: boolean) => void;
}) {
  const { apps, saveApp, getApp, getPublicKey, refresh } = useApps();
  const { activeServer, loading: serversLoading } = useServers();
  const domainsShown = !serversLoading && Boolean(activeServer?.features?.ingress);
  const toServer = useServerPath();
  const [state, setState] = useState<AppEditorState>(emptyEditorState);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // baseline is the loaded state serialized, so "unsaved changes" is a plain
  // comparison rather than per-field tracking.
  const [baseline, setBaseline] = useState("");
  // tried: a save was refused, so every field shows its message (and clears
  // it as it is fixed); refused counts the refusals, so each one opens the
  // sections holding a bad field again.
  const [tried, setTried] = useState(false);
  const [refused, setRefused] = useState(0);
  // stale: the app changed (History, an update) while this editor held edits.
  const [stale, setStale] = useState(false);
  const root = useRef<HTMLDivElement>(null);

  // Only the latest read may land: an older answer arriving last must not
  // overwrite a newer one, nor clear the stale notice it raised.
  const seq = useRef(0);
  // load reads the app: version names the one the list holds, so the
  // page's read of it is shared (readAppDetail); without it the read is
  // afresh (History or an update changed the app under the list).
  const load = useCallback(async (version?: string) => {
    const mine = ++seq.current;
    setLoading(true);
    setError(null);
    try {
      const detail = version === undefined ? await getApp(appId) : await readAppDetail(getApp, appId, version);
      if (mine !== seq.current) return;
      const loaded = stateFromDetail(appId, detail);
      setState(loaded);
      setBaseline(JSON.stringify(loaded));
      setTried(false);
      setStale(false);
    } catch (e) {
      if (mine !== seq.current) return;
      setError(e instanceof Error ? e.message : "Failed to load app");
    } finally {
      if (mine === seq.current) setLoading(false);
    }
  }, [appId, getApp]);

  // The first read is of the version the list holds: the page's read of it.
  const listed = useRef(apps.find((a) => a.id === appId)?.version);
  useEffect(() => {
    void load(listed.current);
  }, [load]);

  const handleSave = async (draft: boolean) => {
    if (stale) return;
    if (Object.keys(panelErrors(state, domainsShown)).length > 0) {
      setTried(true);
      setRefused((n) => n + 1);
      focusFirstInvalid(root.current);
      return;
    }
    setSaving(true);
    try {
      const payload = await buildSavePayload(state, getPublicKey, appId);
      const list = await saveApp({ ...payload, draft });
      toast.success(draft ? "Draft saved — not deployed yet" : "App updated");
      // Reload so masked secrets and server-side normalization are reflected
      // (the saved version: the page reads it too).
      await load(list.find((a) => a.id === appId)?.version);
    } catch (e) {
      const message = e instanceof Error ? e.message : "";
      if (isManagedByTemplate(message)) {
        // A template instance took the app over (the list was stale): its
        // Configuration is the instance's parameters, shown once the list
        // is read again.
        toast.error("This app is managed by a template", {
          description: "Change it through its parameters, or detach it from the template in Settings first.",
        });
        void refresh();
        return;
      }
      toast.error(draft ? "Failed to save draft" : "Failed to update app", {
        description: message || undefined,
      });
    } finally {
      setSaving(false);
    }
  };

  const dirty = useMemo(
    () => baseline !== "" && JSON.stringify(state) !== baseline,
    [state, baseline],
  );

  useEffect(() => {
    onDirtyChange?.(dirty);
  }, [dirty, onDirtyChange]);
  useEffect(() => () => onDirtyChange?.(false), [onDirtyChange]);

  // handled is the last reloadSignal acted on; the first value is the
  // mount's, whose load is the initial one.
  const handled = useRef(reloadSignal);
  useEffect(() => {
    if (reloadSignal === handled.current) return;
    handled.current = reloadSignal;
    if (dirty) setStale(true);
    else void load();
  }, [reloadSignal, dirty, load]);

  const errors = tried ? panelErrors(state, domainsShown) : NO_ERRORS;
  const [firstKey, firstMessage] = Object.entries(errors)[0] ?? [];

  if (loading) return <PageSpinner />;

  if (error) return <LoadError message={error} onRetry={() => void load()} />;

  return (
    <div ref={root}>
      <ConfigBar
        status={
          errors.name ? (
            <span role="alert" tabIndex={-1} data-refusal className="text-destructive">
              This app has no name yet.{" "}
              {dirty ? (
                // Opening Settings would drop the edits made here.
                <>Save or discard your changes first, then name it in Settings.</>
              ) : (
                <>
                  <Link to={toServer(appSub(appId, "settings"))} className="font-medium underline underline-offset-4">
                    Name it in Settings
                  </Link>{" "}
                  first.
                </>
              )}
            </span>
          ) : firstKey ? (
            <span role="alert" tabIndex={-1} data-refusal className="text-destructive">
              Some fields need attention.
              {/* A redirect sits under "Advanced": say what is wrong there too. */}
              {firstKey.startsWith("redirect-") ? ` ${firstMessage}` : ""}
            </span>
          ) : dirty ? (
            <span className="inline-flex items-center gap-2 text-foreground">
              <span className="size-2 rounded-full bg-status-warn" aria-hidden />
              Unsaved changes
            </span>
          ) : undefined
        }
        dirty={dirty || stale}
        onOpenHistory={onOpenHistory}
        secondary={[
          {
            label: "Save draft",
            onSelect: () => void handleSave(true),
            disabled: saving || !dirty || stale,
            title: "Save a new version without redeploying",
          },
          { label: "Discard", onSelect: () => void load(), disabled: saving || !dirty },
        ]}
        primary={
          <Button onClick={() => void handleSave(false)} disabled={saving || !dirty || stale}>
            {saving ? "Saving…" : "Save & redeploy"}
          </Button>
        }
      />
      {stale ? (
        <div
          role="alert"
          className="mb-6 flex flex-wrap items-center gap-3 rounded-lg border border-status-warn/50 bg-muted/40 px-4 py-3 text-sm"
        >
          <span className="mr-auto">
            This app changed since you opened it (in History or by an update). Reload to see the current version — your
            unsaved edits will be lost.
          </span>
          <Button size="sm" variant="outline" onClick={() => void load()}>
            Reload
          </Button>
        </div>
      ) : null}
      <AppEditor state={state} onChange={setState} errors={errors} refused={refused} />
    </div>
  );
}
