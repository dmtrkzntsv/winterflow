import { useCallback, useEffect, useState } from "react";
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
  const { createApp, getApp, getPublicKey } = useApps();
  const [state, setState] = useState<AppEditorState>(emptyEditorState);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const detail = await getApp(appId);
      setState(stateFromDetail(appId, detail));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load app");
    } finally {
      setLoading(false);
    }
  }, [appId, getApp]);

  useEffect(() => {
    void load();
  }, [load]);

  const handleSave = async () => {
    const err = validateEditorState(state);
    if (err) {
      toast.error(err);
      return;
    }
    setSaving(true);
    try {
      const payload = await buildSavePayload(state, getPublicKey, appId);
      await createApp(payload);
      toast.success("App updated");
      // Reload so masked secrets and server-side normalization are reflected.
      await load();
    } catch (e) {
      toast.error("Failed to update app", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setSaving(false);
    }
  };

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
    <div className="space-y-4">
      <div className="flex justify-end gap-2">
        <Button
          variant="outline"
          onClick={() => void load()}
          disabled={saving}
        >
          Discard changes
        </Button>
        <Button onClick={() => void handleSave()} disabled={saving}>
          {saving ? "Saving…" : "Save & redeploy"}
        </Button>
      </div>
      <AppEditor state={state} onChange={setState} />
    </div>
  );
}
