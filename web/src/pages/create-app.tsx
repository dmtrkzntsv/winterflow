import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";

import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { Button } from "@/components/ui/button";
import { AppEditor } from "@/components/app-editor";
import { useApps } from "@/context/use-apps";
import {
  buildSavePayload,
  emptyEditorState,
  validateEditorState,
} from "@/lib/app-editor-io";
import type { AppEditorState } from "@/types/app-config";

export default function CreateAppPage() {
  const navigate = useNavigate();
  const { createApp, getPublicKey } = useApps();
  const [state, setState] = useState<AppEditorState>(emptyEditorState);
  const [saving, setSaving] = useState(false);

  const breadcrumbs = useMemo(
    () => [{ label: "Apps", href: "/" }, { label: "Create App" }],
    [],
  );
  useAppBreadcrumbs(breadcrumbs);

  const handleCreate = async () => {
    const err = validateEditorState(state);
    if (err) {
      toast.error(err);
      return;
    }
    setSaving(true);
    try {
      const payload = await buildSavePayload(state, getPublicKey);
      await createApp(payload);
      toast.success("App created");
      navigate("/");
    } catch (e) {
      toast.error("Failed to create app", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="mx-auto w-full max-w-3xl space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold">Create App</h1>
        <div className="flex gap-2">
          <Button
            variant="outline"
            onClick={() => navigate("/")}
            disabled={saving}
          >
            Cancel
          </Button>
          <Button onClick={() => void handleCreate()} disabled={saving}>
            {saving ? "Creating…" : "Create"}
          </Button>
        </div>
      </div>
      <AppEditor state={state} onChange={setState} />
    </div>
  );
}
