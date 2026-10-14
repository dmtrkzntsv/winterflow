import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { AppEditor } from "@/components/app-editor";
import { StickyActionBar } from "@/components/editor-section";
import { LoadError, PageSpinner } from "@/components/page-states";
import { useApps } from "@/context/use-apps";
import {
  buildSavePayload,
  emptyEditorState,
  stateFromDetail,
  validateEditorState,
} from "@/lib/app-editor-io";
import { isManagedByTemplate } from "@/lib/templates";
import type { AppEditorState } from "@/types/app-config";

// AppEditorPanel is the Editor tab on the app details page: it loads the
// app's current revision, lets the user edit compose/files/variables, and
// saves in place (app.save with the app id → new revision + redeploy).
// headerActions (the page's History button) sit in the action bar before
// the Save buttons.
export function AppEditorPanel({ appId, headerActions }: { appId: string; headerActions?: ReactNode }) {
  const { saveApp, getApp, getPublicKey, refresh } = useApps();
  const [state, setState] = useState<AppEditorState>(emptyEditorState);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // baseline is the loaded state serialized, so "unsaved changes" is a plain
  // comparison rather than per-field tracking.
  const [baseline, setBaseline] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const detail = await getApp(appId);
      const loaded = stateFromDetail(appId, detail);
      setState(loaded);
      setBaseline(JSON.stringify(loaded));
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
    const err = validateEditorState(state);
    if (err) {
      toast.error(err);
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
          description: "Change it through its parameters, or unlink it in Settings first.",
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

  if (loading) return <PageSpinner />;

  if (error) return <LoadError message={error} onRetry={() => void load()} />;

  return (
    <div>
      <StickyActionBar className="mb-6">
        <p className="mr-auto text-sm text-muted-foreground">
          {dirty ? (
            <span className="inline-flex items-center gap-2 text-foreground">
              <span className="size-2 rounded-full bg-amber-400" aria-hidden />
              Unsaved changes
            </span>
          ) : (
            "Every save is kept in History, so you can roll back."
          )}
        </p>
        {headerActions}
        <Button
          variant="ghost"
          onClick={() => void load()}
          disabled={saving || !dirty}
        >
          Discard
        </Button>
        <Button
          variant="outline"
          onClick={() => void handleSave(true)}
          disabled={saving || !dirty}
          title="Save a new revision without redeploying"
        >
          Save draft
        </Button>
        <Button
          onClick={() => void handleSave(false)}
          disabled={saving || !dirty}
        >
          {saving ? "Saving…" : "Save & redeploy"}
        </Button>
      </StickyActionBar>
      <AppEditor state={state} onChange={setState} />
    </div>
  );
}
