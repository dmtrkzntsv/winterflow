import { Eye, EyeOff, FileText, Lock, LockOpen, Plus, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import { Switch } from "@/components/ui/switch";
import { PasswordInput } from "@/components/ui/password-input";
import { CodeEditor } from "@/components/code-editor";
import { ImageTagPicker } from "@/components/image-tag-picker";
import { IconPicker } from "@/components/icon-picker";
import { IngressEditor } from "@/components/ingress-editor";
import { EditorSection } from "@/components/editor-section";
import { cn } from "@/lib/utils";
import { useState } from "react";
import { useServers } from "@/context/use-servers";
import {
  localId,
  type AppEditorState,
  type AppFileMeta,
  type AppVariableMeta,
} from "@/types/app-config";

type Props = {
  state: AppEditorState;
  onChange: (next: AppEditorState) => void;
};

// AppEditor edits an app's compose files and variables (v1 parity, minus
// extensions). Secrets are flagged with is_encrypted and encrypted on submit by
// the page; the editor only manages plaintext entry + masking.
export function AppEditor({ state, onChange }: Props) {
  const [revealed, setRevealed] = useState<Record<string, boolean>>({});
  const { activeServer, loading: serversLoading } = useServers();

  const setConfig = (patch: Partial<AppEditorState["config"]>) =>
    onChange({ ...state, config: { ...state.config, ...patch } });

  // --- files ---
  const updateFileMeta = (id: string, patch: Partial<AppFileMeta>) =>
    setConfig({
      files: state.config.files.map((f) =>
        f.id === id ? { ...f, ...patch } : f,
      ),
    });

  const updateFileContent = (id: string, content: string) =>
    onChange({ ...state, files: { ...state.files, [id]: content } });

  const addFile = () => {
    const id = localId("file");
    onChange({
      ...state,
      config: {
        ...state.config,
        files: [
          ...state.config.files,
          { id, filename: "", is_encrypted: false },
        ],
      },
      files: { ...state.files, [id]: "" },
    });
  };

  const removeFile = (id: string) => {
    const rest = { ...state.files };
    delete rest[id];
    onChange({
      ...state,
      config: {
        ...state.config,
        files: state.config.files.filter((f) => f.id !== id),
      },
      files: rest,
    });
  };

  // --- variables ---
  const updateVarMeta = (id: string, patch: Partial<AppVariableMeta>) =>
    setConfig({
      variables: state.config.variables.map((v) =>
        v.id === id ? { ...v, ...patch } : v,
      ),
    });

  const updateVarValue = (id: string, value: string) =>
    onChange({ ...state, variables: { ...state.variables, [id]: value } });

  const addVar = () => {
    const id = localId("var");
    onChange({
      ...state,
      config: {
        ...state.config,
        variables: [
          ...state.config.variables,
          { id, name: "", is_encrypted: false },
        ],
      },
      variables: { ...state.variables, [id]: "" },
    });
  };

  const removeVar = (id: string) => {
    const rest = { ...state.variables };
    delete rest[id];
    onChange({
      ...state,
      config: {
        ...state.config,
        variables: state.config.variables.filter((v) => v.id !== id),
      },
      variables: rest,
    });
  };

  const source = state.config.source;
  const setSource = (patch: Partial<NonNullable<typeof source>>) =>
    setConfig({ source: { ...source!, ...patch } });

  return (
    <div>
      <EditorSection
        title="Details"
        description="How the app appears in the sidebar and app list."
      >
        {/* Labels on one row, controls on the next, so the taller icon
            button centers against the inputs instead of hanging below. */}
        <div className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2">
          <Label>Icon</Label>
          <Label htmlFor="app-name">Name</Label>
          <Label htmlFor="app-color">Color</Label>
          <IconPicker
            value={state.config.icon}
            color={state.config.color}
            onChange={(icon) => setConfig({ icon })}
          />
          <Input
            id="app-name"
            value={state.config.name}
            placeholder="my-app"
            onChange={(e) => setConfig({ name: e.target.value })}
          />
          <Input
            id="app-color"
            type="color"
            className="h-9 w-14 cursor-pointer p-1"
            value={state.config.color || "#64748b"}
            onChange={(e) => setConfig({ color: e.target.value })}
          />
        </div>
        <div className="grid gap-2">
          <Label htmlFor="app-desc">Description</Label>
          <Input
            id="app-desc"
            value={state.config.description || ""}
            placeholder="Optional"
            onChange={(e) => setConfig({ description: e.target.value })}
          />
        </div>
      </EditorSection>

      <EditorSection
        title="Deploy from Git"
        description="Clone a repository and run its compose file. When off, the files below are the whole app."
        inlineAction
        action={
          <Switch
            aria-label="Deploy from Git"
            checked={Boolean(source)}
            onCheckedChange={(on) =>
              setConfig({
                source: on
                  ? source ?? {
                      repo_url: "",
                      branch: "main",
                      compose_path: "",
                      auto_update: true,
                      poll_seconds: 120,
                    }
                  : undefined,
              })
            }
          />
        }
      >
        {source ? (
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="grid gap-2 sm:col-span-2">
              <Label htmlFor="src-url">Repository URL</Label>
              <Input
                id="src-url"
                value={source.repo_url}
                placeholder="https://github.com/org/app"
                className="font-mono"
                onChange={(e) => setSource({ repo_url: e.target.value })}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="src-branch">Branch</Label>
              <Input
                id="src-branch"
                value={source.branch}
                placeholder="main"
                className="font-mono"
                onChange={(e) => setSource({ branch: e.target.value })}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="src-compose">Compose file path</Label>
              <Input
                id="src-compose"
                value={source.compose_path || ""}
                placeholder="Repository root by default"
                className="font-mono"
                onChange={(e) => setSource({ compose_path: e.target.value })}
              />
            </div>
            <div className="grid gap-2 sm:col-span-2">
              <Label htmlFor="src-token">Access token (private repos)</Label>
              <PasswordInput
                id="src-token"
                value={state.sourceToken || ""}
                placeholder={source.token_set ? "Stored — leave blank to keep" : "Optional"}
                onChange={(e) => onChange({ ...state, sourceToken: e.target.value })}
              />
            </div>
            <label className="flex items-center gap-2 text-sm sm:col-span-2">
              <Checkbox
                checked={source.auto_update}
                onCheckedChange={(c) => setSource({ auto_update: c === true })}
              />
              Redeploy automatically when the branch gets new commits
            </label>
            <p className="text-xs text-muted-foreground sm:col-span-2">
              No compose file in the repository? Add a root <code>compose.yml</code> under
              Files — the clone is available at <code>./source</code>.
            </p>
          </div>
        ) : null}
      </EditorSection>

      <EditorSection
        title="Files"
        description={
          <>
            Written into the app directory. Mark a file secret to encrypt it for
            this server only.
          </>
        }
        action={
          <Button size="sm" variant="outline" onClick={addFile}>
            <Plus className="size-4" /> Add file
          </Button>
        }
      >
        {state.config.files.length === 0 ? (
          <p className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">
            Add at least a <code>compose.yml</code>.
          </p>
        ) : null}
        {state.config.files.map((f) => (
          <div key={f.id} className="overflow-hidden rounded-md border">
            <div className="flex items-center gap-1 border-b bg-muted/40 py-1 pr-1 pl-1">
              <FileText className="ml-2 size-4 shrink-0 text-muted-foreground" />
              <Input
                aria-label="File name"
                value={f.filename}
                placeholder="compose.yml"
                className="h-8 border-transparent bg-transparent font-mono shadow-none focus-visible:border-input focus-visible:bg-background"
                onChange={(e) => updateFileMeta(f.id, { filename: e.target.value })}
              />
              <SecretToggle
                on={f.is_encrypted}
                onChange={(on) => updateFileMeta(f.id, { is_encrypted: on })}
              />
              <Button
                size="icon"
                variant="ghost"
                className="size-8 shrink-0 text-muted-foreground hover:text-destructive"
                aria-label={`Remove ${f.filename || "file"}`}
                onClick={() => removeFile(f.id)}
              >
                <Trash2 className="size-4" />
              </Button>
            </div>
            <CodeEditor
              value={state.files[f.id] ?? ""}
              onChange={(content) => updateFileContent(f.id, content)}
              filename={f.filename}
              placeholder="file contents (use ${VAR} for variables)"
              className="rounded-none border-0"
            />
            {isComposeFilename(f.filename) ? (
              <ImageChips
                content={state.files[f.id] ?? ""}
                onReplace={(oldRef, newRef) =>
                  updateFileContent(
                    f.id,
                    (state.files[f.id] ?? "").split(oldRef).join(newRef),
                  )
                }
              />
            ) : null}
          </div>
        ))}
      </EditorSection>

      <EditorSection
        title="Variables"
        description={
          <>
            Substituted into files as <code>{"${NAME}"}</code>. Secret values
            are encrypted in your browser.
          </>
        }
        action={
          <Button size="sm" variant="outline" onClick={addVar}>
            <Plus className="size-4" /> Add variable
          </Button>
        }
      >
        {state.config.variables.length === 0 ? (
          <p className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">
            No variables yet.
          </p>
        ) : (
          <div className="divide-y rounded-md border">
            {state.config.variables.map((v) => {
              const isRevealed = revealed[v.id] || !v.is_encrypted;
              return (
                <div key={v.id} className="flex items-center gap-1 p-1">
                  <Input
                    aria-label="Variable name"
                    value={v.name}
                    placeholder="VAR_NAME"
                    className="h-8 w-32 shrink-0 border-transparent font-mono shadow-none focus-visible:border-input sm:w-56"
                    onChange={(e) => updateVarMeta(v.id, { name: e.target.value })}
                  />
                  <Input
                    aria-label={`Value of ${v.name || "variable"}`}
                    value={state.variables[v.id] ?? ""}
                    type={isRevealed ? "text" : "password"}
                    placeholder="value"
                    className="h-8 border-transparent font-mono shadow-none focus-visible:border-input"
                    onChange={(e) => updateVarValue(v.id, e.target.value)}
                  />
                  {v.is_encrypted ? (
                    <Button
                      size="icon"
                      variant="ghost"
                      className="size-8 shrink-0 text-muted-foreground"
                      aria-label={isRevealed ? "Hide value" : "Show value"}
                      onClick={() =>
                        setRevealed((r) => ({ ...r, [v.id]: !r[v.id] }))
                      }
                    >
                      {isRevealed ? (
                        <EyeOff className="size-4" />
                      ) : (
                        <Eye className="size-4" />
                      )}
                    </Button>
                  ) : null}
                  <SecretToggle
                    on={v.is_encrypted}
                    onChange={(on) => updateVarMeta(v.id, { is_encrypted: on })}
                  />
                  <Button
                    size="icon"
                    variant="ghost"
                    className="size-8 shrink-0 text-muted-foreground hover:text-destructive"
                    aria-label={`Remove ${v.name || "variable"}`}
                    onClick={() => removeVar(v.id)}
                  >
                    <Trash2 className="size-4" />
                  </Button>
                </div>
              );
            })}
          </div>
        )}
      </EditorSection>

      {/* activeServer is null while get-servers is in flight; render neither
          section until it resolves so supported servers don't flash the
          unsupported fallback. */}
      {serversLoading ? null : activeServer?.features?.ingress ? (
        <IngressEditor
          state={state}
          onChange={onChange}
          appId={state.config.id || undefined}
        />
      ) : (
        <EditorSection
          title="Domains & Routing"
          description="Ingress isn't enabled on this server, so domains can't be configured here. Enable the built-in proxy (it needs ports 80/443) or route traffic with your own proxy or tunnel."
        />
      )}
    </div>
  );
}

// SecretToggle marks a file or variable as secret (encrypted end to end).
function SecretToggle({
  on,
  onChange,
}: {
  on: boolean;
  onChange: (on: boolean) => void;
}) {
  return (
    <Button
      type="button"
      size="sm"
      variant="ghost"
      aria-pressed={on}
      title={on ? "Secret: encrypted for this server" : "Mark as secret"}
      className={cn(
        "h-8 shrink-0 gap-1.5 px-2 text-xs",
        on
          ? "bg-amber-500/10 text-amber-700 hover:bg-amber-500/15 hover:text-amber-800 dark:text-amber-400"
          : "text-muted-foreground",
      )}
      onClick={() => onChange(!on)}
    >
      {on ? <Lock className="size-3.5" /> : <LockOpen className="size-3.5" />}
      <span className="sr-only sm:not-sr-only">Secret</span>
    </Button>
  );
}

// isComposeFilename gates the image-tag chips to compose files.
function isComposeFilename(name: string): boolean {
  const n = name.trim();
  return (
    n === "compose.yml" ||
    n === "compose.yaml" ||
    n === "docker-compose.yml" ||
    n === "docker-compose.yaml"
  );
}

const IMAGE_LINE = /^\s*image:\s*["']?([\w][\w./:@-]*)/gm;

// ImageChips lists the image references found in a compose file, each with a
// tag browser that rewrites the reference in place.
function ImageChips({
  content,
  onReplace,
}: {
  content: string;
  onReplace: (oldRef: string, newRef: string) => void;
}) {
  const refs = Array.from(
    new Set(
      Array.from(content.matchAll(IMAGE_LINE), (m) => m[1]).filter(Boolean),
    ),
  );
  if (refs.length === 0) return null;
  return (
    <div className="flex flex-wrap items-center gap-2 border-t bg-muted/20 px-3 py-2">
      <span className="text-xs text-muted-foreground">Images</span>
      {refs.map((ref) => (
        <ImageTagPicker
          key={ref}
          image={ref}
          onSelect={(newRef) => onReplace(ref, newRef)}
        />
      ))}
    </div>
  );
}
