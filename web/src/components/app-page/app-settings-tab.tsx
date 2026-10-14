import { useState } from "react";
import { Trash2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { Ownership } from "@/components/app-page/ownership";
import { ConfirmButton } from "@/components/confirm-button";
import { useTemplatesApi } from "@/hooks/use-templates-api";

export function AppSettingsTab({
  appId,
  name,
  ownership,
  onUnlinked,
  onRename,
  onDelete,
  onSaveAsTemplate,
  savingTemplate,
}: {
  appId: string;
  name: string;
  // A templated app (ownership set, its instance perhaps still loading)
  // can't be offered as the source of a new template.
  ownership: Ownership;
  onUnlinked: () => void;
  onRename: (appId: string, name: string) => Promise<void>;
  // onDelete opens the page's delete dialog; onSaveAsTemplate runs the page's
  // hook (savingTemplate while the app is read).
  onDelete: () => void;
  onSaveAsTemplate: () => void;
  savingTemplate: boolean;
}) {
  const instance = ownership?.instance ?? null;
  const templatesApi = useTemplatesApi();
  const [newName, setNewName] = useState(name);
  const [busy, setBusy] = useState(false);

  const save = async () => {
    setBusy(true);
    try {
      await onRename(appId, newName.trim());
      toast.success("App renamed");
    } catch (e) {
      toast.error("Rename failed", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  const unlink = async () => {
    if (!instance) return;
    setBusy(true);
    try {
      await templatesApi.unlinkInstance(instance.server_id, instance.key);
      toast.success("Unlinked from the template");
      onUnlinked();
    } catch (e) {
      toast.error("Unlink failed", { description: e instanceof Error ? e.message : undefined });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="max-w-4xl divide-y rounded-lg border">
      <div className="grid gap-4 p-5 md:grid-cols-[14rem_minmax(0,1fr)] md:gap-8">
        <div className="space-y-1">
          <h2 className="text-sm font-semibold">Name</h2>
          <p className="text-sm text-muted-foreground">
            Shown in the sidebar and app list. The app's containers keep running.
          </p>
        </div>
        <form
          className="flex items-center gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            void save();
          }}
        >
          <Input
            id="app-name"
            aria-label="App name"
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
          />
          <Button
            type="submit"
            disabled={busy || !newName.trim() || newName === name}
          >
            Rename
          </Button>
        </form>
      </div>

      {instance ? (
        <div className="grid gap-4 p-5 md:grid-cols-[14rem_minmax(0,1fr)] md:gap-8">
          <div className="space-y-1">
            <h2 className="text-sm font-semibold">Template</h2>
            <p className="text-sm text-muted-foreground">
              Managed by template {instance.template_name}: its files and values change through the template.
            </p>
          </div>
          {instance.state !== "created" ? (
            // The API unlinks only a created instance.
            <p className="text-sm text-muted-foreground">
              Unlink becomes available once its first deploy from the template succeeds.
            </p>
          ) : (
            <div>
              <ConfirmButton
                trigger={
                  <Button variant="outline" disabled={busy}>
                    Unlink from template
                  </Button>
                }
                title={`Unlink from ${instance.template_name}?`}
                description="The app becomes an ordinary app you can edit freely; template upgrades no longer reach it."
                confirmLabel="Unlink"
                onConfirm={() => void unlink()}
              />
            </div>
          )}
        </div>
      ) : ownership ? null : (
        <div className="grid gap-4 p-5 md:grid-cols-[14rem_minmax(0,1fr)] md:gap-8">
          <div className="space-y-1">
            <h2 className="text-sm font-semibold">Save as template</h2>
            <p className="text-sm text-muted-foreground">
              Start a template from this app: its files, its variables as parameters, its hosts and backup settings.
              The app stays as it is.
            </p>
          </div>
          <div>
            <Button variant="outline" disabled={busy || savingTemplate} onClick={onSaveAsTemplate}>
              Save as template
            </Button>
          </div>
        </div>
      )}

      <div className="grid gap-4 p-5 md:grid-cols-[14rem_minmax(0,1fr)] md:gap-8">
        <div className="space-y-1">
          <h2 className="text-sm font-semibold text-destructive">Delete app</h2>
          <p className="text-sm text-muted-foreground">
            Stops the containers and removes the deployment and its history.
            Volumes and backups are kept unless you choose otherwise.
          </p>
        </div>
        <div>
          <Button
            variant="outline"
            className="border-destructive/40 text-destructive hover:bg-destructive/10 hover:text-destructive"
            disabled={busy}
            onClick={onDelete}
          >
            <Trash2 className="size-4" aria-hidden /> Delete app…
          </Button>
        </div>
      </div>
    </div>
  );
}
