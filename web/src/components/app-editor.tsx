import { Lock } from "lucide-react";

import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import { Switch } from "@/components/ui/switch";
import { PasswordInput } from "@/components/ui/password-input";
import { FilesSection } from "@/components/files-section";
import { VariablesEditor } from "@/components/variables-editor";
import { IconPicker } from "@/components/icon-picker";
import { IngressEditor } from "@/components/ingress-editor";
import { EditorSection } from "@/components/editor-section";
import { ENCRYPTED } from "@/lib/ecies";
import { SharedVariablesHint } from "@/components/shared-variables-hint";
import { useSharedNames } from "@/hooks/use-shared-names";
import type { FieldErrors } from "@/lib/field-errors";
import { useServers } from "@/context/use-servers";
import type { AppEditorState, StorageVariable } from "@/types/app-config";

type Props = {
  state: AppEditorState;
  onChange: (next: AppEditorState) => void;
  errors?: FieldErrors;
};

// AppEditor edits an app's compose files and variables (v1 parity, minus
// extensions). Secrets are flagged with is_encrypted and encrypted on submit by
// the page; the editor only manages plaintext entry + masking.
export function AppEditor({ state, onChange, errors = {} }: Props) {
  const { activeServer, loading: serversLoading } = useServers();
  const shared = useSharedNames(activeServer?.id);

  const setConfig = (patch: Partial<AppEditorState["config"]>) =>
    onChange({ ...state, config: { ...state.config, ...patch } });

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
            name={state.config.name}
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

      <FilesSection
        errors={errors}
        files={state.config.files}
        contents={state.files}
        onChange={(files, contents) =>
          onChange({ ...state, config: { ...state.config, files }, files: contents })
        }
        description={
          <>
            Written into the app directory. Mark a file secret to encrypt it for
            this server only.
          </>
        }
      />

      <EditorSection
        title="Variables"
        description={
          <>
            Substituted into files as <code>{"${NAME}"}</code>, including org and
            server variables. Secret values are encrypted in your browser.
          </>
        }
      >
        <VariablesEditor
          state={state}
          onChange={onChange}
          shared={shared}
          errors={errors}
          emptyNote={state.storageVariables.length === 0}
        />
        <SharedVariablesHint shared={shared} />
        <StorageVariableRows vars={state.storageVariables} />
      </EditorSection>

      {/* activeServer is null while get-servers is in flight; render neither
          section until it resolves so supported servers don't flash the
          unsupported fallback. */}
      {serversLoading ? null : activeServer?.features?.ingress ? (
        <IngressEditor
          state={state}
          onChange={onChange}
          appId={state.config.id || undefined}
          serverId={activeServer.id}
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

// StorageVariableRows lists the WF_STORAGE_ variables the compose files
// reference, read-only: their values come from the server's storages (Settings
// → Storages), and a secret shows only as <encrypted>.
function StorageVariableRows({ vars }: { vars: StorageVariable[] }) {
  if (vars.length === 0) return null;
  return (
    <ul aria-label="Storage variables" className="mt-2 divide-y rounded-md border bg-muted/30">
      {vars.map((v) => (
        <li key={v.name} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-sm">
          <code className="font-mono [overflow-wrap:anywhere]">{v.name}</code>
          {v.secret ? (
            <span className="flex items-center gap-1 font-mono text-xs text-muted-foreground">
              <Lock className="size-3" /> {ENCRYPTED}
            </span>
          ) : null}
          <span className="ml-auto text-xs text-muted-foreground">
            provided by storage <code className="font-mono">{v.storage}</code>
          </span>
        </li>
      ))}
    </ul>
  );
}
