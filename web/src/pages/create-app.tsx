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
  const { saveApp, getPublicKey } = useApps();
  const [state, setState] = useState<AppEditorState>(emptyEditorState);
  const [saving, setSaving] = useState(false);

  const breadcrumbs = useMemo(
    () => [{ label: "Apps", href: "/" }, { label: "New app" }],
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
      const apps = await saveApp(payload);
      toast.success("App created and deploying");
      // Land on the new app's page so its deployment is visible right away.
      const created = apps.find((a) => a.name === state.config.name.trim());
      navigate(created ? `/app/${created.id}` : "/");
    } catch (e) {
      toast.error("Couldn't create the app", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="mx-auto w-full max-w-5xl">
      {/* Sticky so Create stays reachable at the bottom of a long form. */}
      <div className="sticky top-0 z-10 -mx-4 mb-6 flex flex-wrap items-center gap-x-4 gap-y-2 border-b bg-background/95 px-4 py-3 backdrop-blur supports-[backdrop-filter]:bg-background/80">
        <div className="mr-auto min-w-0">
          <h1 className="text-xl font-semibold tracking-tight">New app</h1>
          <p className="text-sm text-muted-foreground">
            Describe it with a compose file; it deploys as soon as you create it.
          </p>
        </div>
        <Button
          variant="ghost"
          onClick={() => navigate("/")}
          disabled={saving}
        >
          Cancel
        </Button>
        <Button onClick={() => void handleCreate()} disabled={saving}>
          {saving ? "Creating…" : "Create app"}
        </Button>
      </div>
      <AppEditor state={state} onChange={setState} />
    </div>
  );
}
