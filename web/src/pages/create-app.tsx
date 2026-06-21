import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";

import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { Button } from "@/components/ui/button";
import { AppEditor } from "@/components/app-editor";
import { useApps } from "@/context/use-apps";
import { encryptSecret } from "@/lib/ecies";
import {
  DEFAULT_COMPOSE,
  localId,
  type AppEditorState,
} from "@/types/app-config";

function initialState(): AppEditorState {
  const composeId = localId("file");
  return {
    config: {
      id: "",
      name: "",
      description: "",
      color: "#64748b",
      files: [{ id: composeId, filename: "compose.yml", is_encrypted: false }],
      variables: [],
    },
    files: { [composeId]: DEFAULT_COMPOSE },
    variables: {},
  };
}

export default function CreateAppPage() {
  const navigate = useNavigate();
  const { createApp, getPublicKey } = useApps();
  const [state, setState] = useState<AppEditorState>(initialState);
  const [saving, setSaving] = useState(false);

  const breadcrumbs = useMemo(
    () => [{ label: "Apps", href: "/" }, { label: "Create App" }],
    [],
  );
  useAppBreadcrumbs(breadcrumbs);

  const validate = (): string | null => {
    if (!state.config.name.trim()) return "App name is required.";
    const hasCompose = state.config.files.some(
      (f) => f.filename.trim() === "compose.yml" || f.filename.trim() === "docker-compose.yml",
    );
    if (!hasCompose) return "A compose.yml (or docker-compose.yml) file is required.";
    for (const f of state.config.files) {
      if (!f.filename.trim()) return "Every file needs a filename.";
    }
    for (const v of state.config.variables) {
      if (!v.name.trim()) return "Every variable needs a name.";
    }
    return null;
  };

  const handleCreate = async () => {
    const err = validate();
    if (err) {
      toast.error(err);
      return;
    }
    setSaving(true);
    try {
      // Encrypt secret values/files with the server's public key. Non-secrets
      // go as plaintext.
      const needsKey =
        state.config.files.some((f) => f.is_encrypted) ||
        state.config.variables.some((v) => v.is_encrypted);
      const publicKey = needsKey ? await getPublicKey() : "";

      const files = await Promise.all(
        state.config.files.map(async (f) => {
          const content = state.files[f.id] ?? "";
          return {
            name: f.filename.trim(),
            encrypted: f.is_encrypted,
            content: f.is_encrypted
              ? await encryptSecret(content, publicKey)
              : content,
          };
        }),
      );

      const variables = await Promise.all(
        state.config.variables.map(async (v) => {
          const value = state.variables[v.id] ?? "";
          return {
            name: v.name.trim(),
            encrypted: v.is_encrypted,
            content: v.is_encrypted
              ? await encryptSecret(value, publicKey)
              : value,
          };
        }),
      );

      // Strip content from the config metadata before sending; the agent stores
      // it as the app record (config.json).
      const config = {
        name: state.config.name.trim(),
        description: state.config.description || "",
        icon: state.config.icon || "",
        color: state.config.color || "",
        files: state.config.files.map((f) => ({
          filename: f.filename.trim(),
          is_encrypted: f.is_encrypted,
        })),
        variables: state.config.variables.map((v) => ({
          name: v.name.trim(),
          is_encrypted: v.is_encrypted,
        })),
      };

      await createApp({
        app: {
          name: state.config.name.trim(),
          icon: state.config.icon || "",
          color: state.config.color || "",
        },
        config,
        files,
        variables,
      });

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
          <Button variant="outline" onClick={() => navigate("/")} disabled={saving}>
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
