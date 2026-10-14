import { useEffect, useRef, useState } from "react";
import { Lock } from "lucide-react";

import { Switch } from "@/components/ui/switch";
import { Disclosure } from "@/components/disclosure";
import { EditorSection } from "@/components/editor-section";
import { FilesSection } from "@/components/files-section";
import { GitSourceFields } from "@/components/git-source-fields";
import { ImageVersions } from "@/components/image-versions";
import { AddressRows, RedirectRows } from "@/components/ingress-editor";
import { SharedVariablesHint } from "@/components/shared-variables-hint";
import { VariablesEditor } from "@/components/variables-editor";
import { useServers } from "@/context/use-servers";
import { useRegistrations } from "@/hooks/use-registrations";
import { useSharedNames } from "@/hooks/use-shared-names";
import { newGitSource } from "@/lib/app-editor-io";
import { ENCRYPTED } from "@/lib/ecies";
import { imageRefs } from "@/lib/compose-images";
import { hasErrorIn, type FieldErrors } from "@/lib/field-errors";
import {
  isComposeFile,
  type AppEditorState,
  type AppSourceConfig,
  type StorageVariable,
} from "@/types/app-config";

type Props = {
  state: AppEditorState;
  onChange: (next: AppEditorState) => void;
  // errors are editorErrors' messages, each shown by its field.
  errors?: FieldErrors;
  // refused counts the saves the panel refused: each one opens "Advanced"
  // when a redirect in it is bad (opened for good, so fixing the field
  // doesn't fold it away under the cursor).
  refused?: number;
};

// AppEditor is the Configuration tab's body: only what runs — files,
// variables, domains and the Git source — with redirects and image
// versions under "Advanced". The app's name, icon, colour and description
// live in its Settings. Secrets are flagged here and encrypted by the save.
export function AppEditor({ state, onChange, errors = {}, refused = 0 }: Props) {
  const { activeServer, loading: serversLoading } = useServers();
  const serverId = activeServer?.id ?? "";
  const ingressOn = Boolean(activeServer?.features?.ingress);
  const shared = useSharedNames(activeServer?.id);
  const { registrations, refresh } = useRegistrations(serverId, ingressOn);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [seenRefused, setSeenRefused] = useState(refused);
  if (refused !== seenRefused) {
    setSeenRefused(refused);
    if (hasErrorIn(errors, "redirect-")) setAdvancedOpen(true);
  }
  const rows = {
    state,
    onChange,
    appId: state.config.id || undefined,
    serverId,
    registrations,
    onRefresh: refresh,
    errors,
  };
  const hasImages = state.config.files.some(
    (f) => isComposeFile(f.filename) && imageRefs(state.files[f.id] ?? "").length > 0,
  );

  return (
    <div>
      <FilesSection
        errors={errors}
        files={state.config.files}
        contents={state.files}
        onChange={(files, contents) =>
          onChange({ ...state, config: { ...state.config, files }, files: contents })
        }
        description="Written into the app's folder. Mark a file secret to encrypt it for this server only."
        emptyNote={
          state.config.source ? "The repository's compose file runs; add files here only to override it." : undefined
        }
        imageChips={false}
      />

      <EditorSection
        title="Variables"
        description={
          <>
            Used in files as <code>{"${NAME}"}</code>, including organization and server variables. Secret values are
            encrypted in your browser.
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

      {/* activeServer is null while get-servers is in flight: render
          nothing until it resolves so a server with the proxy doesn't
          flash the "no proxy" sentence. */}
      {serversLoading ? null : (
        <EditorSection
          title="Domains"
          description={
            ingressOn
              ? "Where people reach the app. Each domain sets how HTTPS works."
              : "This server has no built-in web proxy, so domains can't be set here. People reach the app on the port its compose file publishes, or through your own proxy or tunnel."
          }
        >
          {ingressOn ? (
            <div className="space-y-2">
              <h3 className="text-sm font-medium">Addresses</h3>
              <AddressRows {...rows} />
            </div>
          ) : null}
        </EditorSection>
      )}

      <GitSource state={state} onChange={onChange} errors={errors} />

      {ingressOn || hasImages ? (
        <Disclosure label="Advanced" open={advancedOpen} onOpenChange={setAdvancedOpen} className="border-t pt-4">
          {ingressOn ? (
            <EditorSection title="Redirects" description="Send a hostname, or a path on it, to another address.">
              <RedirectRows {...rows} />
            </EditorSection>
          ) : null}
          {hasImages ? (
            <EditorSection title="Image versions" description="Pick another tag for an image a compose file uses.">
              <ImageVersions
                files={state.config.files}
                contents={state.files}
                onReplace={(fileId, oldRef, newRef) =>
                  onChange({
                    ...state,
                    files: { ...state.files, [fileId]: (state.files[fileId] ?? "").split(oldRef).join(newRef) },
                  })
                }
              />
            </EditorSection>
          ) : null}
        </Disclosure>
      ) : null}
    </div>
  );
}

// GitSource is the app's Git source when it has one; otherwise a closed
// "Deploy from Git" holding the switch that adds one. The switch lives in a
// different place in the two states, so toggling it hands keyboard focus to
// its counterpart (the new switch, or the closed disclosure's button).
function GitSource({
  state,
  onChange,
  errors,
}: {
  state: AppEditorState;
  onChange: (next: AppEditorState) => void;
  errors: FieldErrors;
}) {
  const source = state.config.source;
  const setSource = (next: AppSourceConfig | undefined) =>
    onChange({ ...state, config: { ...state.config, source: next } });
  const root = useRef<HTMLDivElement>(null);
  const keepFocus = useRef(false);
  const hasSource = Boolean(source);
  useEffect(() => {
    if (!keepFocus.current) return;
    keepFocus.current = false;
    const el = root.current?.querySelector<HTMLElement>('[role="switch"]') ?? root.current?.querySelector<HTMLElement>("button");
    el?.focus();
  }, [hasSource]);
  const toggle = (
    <Switch
      aria-label="Deploy from Git"
      checked={Boolean(source)}
      onCheckedChange={(on) => {
        keepFocus.current = true;
        setSource(on ? (source ?? newGitSource()) : undefined);
      }}
    />
  );
  return (
    <div ref={root}>
      {source ? (
        <EditorSection
          title="Git source"
          description="The repository the app runs. Turn it off to run only the files above."
          inlineAction
          action={toggle}
        >
          <GitSourceFields
            source={source}
            token={state.sourceToken || ""}
            onSource={(patch) => setSource({ ...source, ...patch })}
            onToken={(sourceToken) => onChange({ ...state, sourceToken })}
            errors={errors}
          />
          <p className="text-xs text-muted-foreground">
            No compose file in the repository? Add a root <code>compose.yml</code> under Files — the clone is available
            at <code>./source</code>.
          </p>
        </EditorSection>
      ) : (
        <Disclosure label="Deploy from Git" className="border-t pt-4">
          <div className="flex items-start justify-between gap-4 rounded-md border p-3">
            <p className="text-sm text-muted-foreground">
              Clone a repository and run its compose file, redeploying on new commits. While this is off, the files
              above are the whole app.
            </p>
            {toggle}
          </div>
        </Disclosure>
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
