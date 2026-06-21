import { useEffect, useMemo, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { toast } from "sonner";

import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { AppEditor } from "@/components/app-editor";
import { useApps } from "@/context/use-apps";
import { encryptSecret } from "@/lib/ecies";
import type { AppDetailPayload } from "@/context/apps-context-base";
import {
  DEFAULT_COMPOSE,
  localId,
  type AppEditorState,
} from "@/types/app-config";

function emptyState(): AppEditorState {
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

// b64ToString decodes a base64 string to UTF-8 (Go encodes []byte as base64 in
// JSON). Falls back to the raw value if it isn't valid base64.
function b64ToString(b64: string): string {
  try {
    return new TextDecoder().decode(
      Uint8Array.from(atob(b64), (c) => c.charCodeAt(0)),
    );
  } catch {
    return b64;
  }
}

// stateFromDetail builds editor state from an app.get payload. Config metadata
// (which items are secret) comes from the stored config blob; content for files
// and the values for variables come from the payload arrays.
function stateFromDetail(appId: string, d: AppDetailPayload): AppEditorState {
  const cfgRaw = d.app.config ? b64ToString(d.app.config) : "{}";
  let cfg: {
    name?: string;
    description?: string;
    icon?: string;
    color?: string;
    files?: { filename: string; is_encrypted?: boolean }[];
    variables?: { name: string; is_encrypted?: boolean }[];
  } = {};
  try {
    cfg = JSON.parse(cfgRaw);
  } catch {
    cfg = {};
  }

  const encFile = new Map(
    (cfg.files ?? []).map((f) => [f.filename, !!f.is_encrypted]),
  );
  const encVar = new Map(
    (cfg.variables ?? []).map((v) => [v.name, !!v.is_encrypted]),
  );

  const files: AppEditorState["files"] = {};
  const fileMetas = (d.app.files ?? []).map((f) => {
    const id = localId("file");
    const encrypted = f.encrypted ?? encFile.get(f.name) ?? false;
    // Encrypted content is masked by the agent ("<encrypted>"); keep it as the
    // placeholder so save preserves the stored secret unless re-entered.
    files[id] = encrypted ? "<encrypted>" : b64ToString(f.content);
    return { id, filename: f.name, is_encrypted: encrypted };
  });

  const variables: AppEditorState["variables"] = {};
  const varMetas = (d.app.variables ?? []).map((v) => {
    const id = localId("var");
    const encrypted = v.encrypted ?? encVar.get(v.name) ?? false;
    variables[id] = encrypted ? "<encrypted>" : b64ToString(v.content);
    return { id, name: v.name, is_encrypted: encrypted };
  });

  return {
    config: {
      id: appId,
      name: cfg.name ?? "",
      description: cfg.description ?? "",
      icon: cfg.icon ?? "",
      color: cfg.color ?? "#64748b",
      files: fileMetas,
      variables: varMetas,
    },
    files,
    variables,
  };
}

export default function CreateAppPage() {
  const navigate = useNavigate();
  const { appId } = useParams();
  const isEdit = Boolean(appId);
  const { createApp, getApp, getPublicKey } = useApps();
  const [state, setState] = useState<AppEditorState>(emptyState);
  const [saving, setSaving] = useState(false);
  const [loading, setLoading] = useState(isEdit);

  const breadcrumbs = useMemo(
    () => [
      { label: "Apps", href: "/" },
      { label: isEdit ? "Edit App" : "Create App" },
    ],
    [isEdit],
  );
  useAppBreadcrumbs(breadcrumbs);

  useEffect(() => {
    if (!appId) return;
    let cancelled = false;
    setLoading(true);
    getApp(appId)
      .then((detail) => {
        if (!cancelled) setState(stateFromDetail(appId, detail));
      })
      .catch((e) => {
        toast.error("Failed to load app", {
          description: e instanceof Error ? e.message : undefined,
        });
        if (!cancelled) navigate("/");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [appId, getApp, navigate]);

  const validate = (): string | null => {
    if (!state.config.name.trim()) return "App name is required.";
    const hasCompose = state.config.files.some(
      (f) =>
        f.filename.trim() === "compose.yml" ||
        f.filename.trim() === "docker-compose.yml",
    );
    if (!hasCompose)
      return "A compose.yml (or docker-compose.yml) file is required.";
    for (const f of state.config.files) {
      if (!f.filename.trim()) return "Every file needs a filename.";
    }
    for (const v of state.config.variables) {
      if (!v.name.trim()) return "Every variable needs a name.";
    }
    return null;
  };

  // mapItem encrypts a secret value/file, passes the "<encrypted>" placeholder
  // through unchanged (preserve the stored secret), and sends plaintext as-is.
  const mapItem = async (
    name: string,
    rawContent: string,
    encrypted: boolean,
    publicKey: string,
  ) => {
    let content = rawContent;
    if (encrypted && rawContent !== "<encrypted>") {
      content = await encryptSecret(rawContent, publicKey);
    }
    return { name: name.trim(), encrypted, content };
  };

  const handleSave = async () => {
    const err = validate();
    if (err) {
      toast.error(err);
      return;
    }
    setSaving(true);
    try {
      const needsKey =
        state.config.files.some(
          (f) => f.is_encrypted && (state.files[f.id] ?? "") !== "<encrypted>",
        ) ||
        state.config.variables.some(
          (v) =>
            v.is_encrypted && (state.variables[v.id] ?? "") !== "<encrypted>",
        );
      const publicKey = needsKey ? await getPublicKey() : "";

      const files = await Promise.all(
        state.config.files.map((f) =>
          mapItem(f.filename, state.files[f.id] ?? "", f.is_encrypted, publicKey),
        ),
      );
      const variables = await Promise.all(
        state.config.variables.map((v) =>
          mapItem(v.name, state.variables[v.id] ?? "", v.is_encrypted, publicKey),
        ),
      );

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

      const app: Record<string, unknown> = {
        name: state.config.name.trim(),
        icon: state.config.icon || "",
        color: state.config.color || "",
      };
      if (isEdit && appId) app.id = appId;

      await createApp({ app, config, files, variables });

      toast.success(isEdit ? "App updated" : "App created");
      navigate("/");
    } catch (e) {
      toast.error(isEdit ? "Failed to update app" : "Failed to create app", {
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

  return (
    <div className="mx-auto w-full max-w-3xl space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold">
          {isEdit ? "Edit App" : "Create App"}
        </h1>
        <div className="flex gap-2">
          <Button
            variant="outline"
            onClick={() => navigate("/")}
            disabled={saving}
          >
            Cancel
          </Button>
          <Button onClick={() => void handleSave()} disabled={saving}>
            {saving
              ? isEdit
                ? "Saving…"
                : "Creating…"
              : isEdit
                ? "Save"
                : "Create"}
          </Button>
        </div>
      </div>
      <AppEditor state={state} onChange={setState} />
    </div>
  );
}
