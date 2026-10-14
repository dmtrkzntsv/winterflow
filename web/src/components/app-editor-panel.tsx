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
// History from the action bar.
export function AppEditorPanel({
  appId,
  onOpenHistory,
}: {
  appId: string;
  onOpenHistory?: () => void;
}) {
  const { saveApp, getApp, getPublicKey, refresh } = useApps();
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
  const form = useRef<HTMLDivElement>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const detail = await getApp(appId);
      const loaded = stateFromDetail(appId, detail);
      setState(loaded);
      setBaseline(JSON.stringify(loaded));
      setTried(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load app");
    } finally {
      setLoading(false);
    }
  }, [appId, getApp]);

  useEffect(() => {
    void load();
  }, [load]);

  const handleSave = async (draft: boolean) => {
    if (Object.keys(panelErrors(state, domainsShown)).length > 0) {
      setTried(true);
      setRefused((n) => n + 1);
      focusFirstInvalid(form.current);
      return;
    }
    setSaving(true);
    try {
      const payload = await buildSavePayload(state, getPublicKey, appId);
      await saveApp({ ...payload, draft });
      toast.success(draft ? "Draft saved — not deployed yet" : "App updated");
      // Reload so masked secrets and server-side normalization are reflected.
      await load();
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

  const errors = tried ? panelErrors(state, domainsShown) : NO_ERRORS;
  const [firstKey, firstMessage] = Object.entries(errors)[0] ?? [];

  if (loading) return <PageSpinner />;

  if (error) return <LoadError message={error} onRetry={() => void load()} />;

  return (
    <div>
      <ConfigBar
        status={
          errors.name ? (
            <span role="alert" className="text-destructive">
              This app has no name yet.{" "}
              <Link to={toServer(appSub(appId, "settings"))} className="font-medium underline underline-offset-4">
                Name it in Settings
              </Link>{" "}
              first.
            </span>
          ) : firstKey ? (
            <span role="alert" className="text-destructive">
              Some fields need attention.
              {/* A redirect sits under "Advanced": say what is wrong there too. */}
              {firstKey.startsWith("redirect-") ? ` ${firstMessage}` : ""}
            </span>
          ) : dirty ? (
            <span className="inline-flex items-center gap-2 text-foreground">
              <span className="size-2 rounded-full bg-status-warn" aria-hidden />
              Unsaved changes
            </span>
          ) : (
            "Every save is kept in History, so you can roll back."
          )
        }
        onOpenHistory={onOpenHistory}
        secondary={[
          {
            label: "Save draft",
            onSelect: () => void handleSave(true),
            disabled: saving || !dirty,
            title: "Save a new version without redeploying",
          },
          { label: "Discard", onSelect: () => void load(), disabled: saving || !dirty },
        ]}
        primary={
          <Button onClick={() => void handleSave(false)} disabled={saving || !dirty}>
            {saving ? "Saving…" : "Save & redeploy"}
          </Button>
        }
      />
      <div ref={form}>
        <AppEditor state={state} onChange={setState} errors={errors} refused={refused} />
      </div>
    </div>
  );
}
