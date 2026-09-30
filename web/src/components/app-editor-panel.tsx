import { useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { AppEditor } from "@/components/app-editor";
import { useApps } from "@/context/use-apps";
import {
  buildSavePayload,
  emptyEditorState,
  stateFromDetail,
  validateEditorState,
} from "@/lib/app-editor-io";
import type { AppEditorState } from "@/types/app-config";

// AppEditorPanel is the Editor tab on the app details page: it loads the
// app's current revision, lets the user edit compose/files/variables, and
// saves in place (app.save with the app id → new revision + redeploy).
export function AppEditorPanel({ appId }: { appId: string }) {
  const { saveApp, getApp, getPublicKey } = useApps();
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
      toast.error(draft ? "Failed to save draft" : "Failed to update app", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setSaving(false);
    }
  };

  const dirty = useMemo(
    () => baseline !== "" && JSON.stringify(state) !== baseline,
    [state, baseline],
  );

  if (loading) {
    return (
      <div className="flex min-h-60 items-center justify-center">
        <Spinner />
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex min-h-60 flex-col items-center justify-center gap-3">
        <p className="text-sm text-destructive">{error}</p>
        <Button variant="outline" size="sm" onClick={() => void load()}>
          Retry
        </Button>
      </div>
    );
  }

  return (
    <div>
      <div className="sticky top-0 z-10 -mx-4 mb-6 flex flex-wrap items-center gap-2 border-b bg-background/95 px-4 py-3 backdrop-blur supports-[backdrop-filter]:bg-background/80">
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
      </div>
      <AppEditor state={state} onChange={setState} />
    </div>
  );
}
