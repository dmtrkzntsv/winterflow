import { Lock } from "lucide-react";

import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { FilesSection } from "@/components/files-section";
import { VariablesEditor } from "@/components/variables-editor";
import { IconPicker } from "@/components/icon-picker";
import { GitSourceFields } from "@/components/git-source-fields";
import { AddressRows, RedirectRows } from "@/components/ingress-editor";
import { EditorSection } from "@/components/editor-section";
import { newGitSource } from "@/lib/app-editor-io";
import { ENCRYPTED } from "@/lib/ecies";
import { SharedVariablesHint } from "@/components/shared-variables-hint";
import { useRegistrations } from "@/hooks/use-registrations";
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
  const ingressOn = Boolean(activeServer?.features?.ingress);
  const { registrations, refresh } = useRegistrations(activeServer?.id ?? "", ingressOn);

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
                  ? source ?? newGitSource()
                  : undefined,
              })
            }
          />
        }
      >
        {source ? (
          <>
            <GitSourceFields
              source={source}
              token={state.sourceToken || ""}
              onSource={setSource}
              onToken={(sourceToken) => onChange({ ...state, sourceToken })}
              errors={errors}
            />
            <p className="text-xs text-muted-foreground">
              No compose file in the repository? Add a root <code>compose.yml</code> under
              Files — the clone is available at <code>./source</code>.
            </p>
          </>
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
        <EditorSection
          title="Domains & Routing"
          description="Serve the app on hostnames under the domains on this server. Each domain sets how HTTPS works."
        >
          <AddressRows
            state={state}
            onChange={onChange}
            appId={state.config.id || undefined}
            serverId={activeServer.id}
            registrations={registrations}
            onRefresh={refresh}
            errors={errors}
          />
          <RedirectRows
            state={state}
            onChange={onChange}
            appId={state.config.id || undefined}
            serverId={activeServer.id}
            registrations={registrations}
            onRefresh={refresh}
            errors={errors}
          />
        </EditorSection>
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
