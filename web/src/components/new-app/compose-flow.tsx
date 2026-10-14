import { useMemo, useState } from "react";

import { Input } from "@/components/ui/input";
import { Disclosure } from "@/components/disclosure";
import { FileEditors } from "@/components/files-section";
import { FieldMessage, FormField } from "@/components/form-field";
import { ImageVersions } from "@/components/image-versions";
import { AddressRows, RedirectRows } from "@/components/ingress-editor";
import { NameField } from "@/components/name-field";
import { CreateBar } from "@/components/new-app/create-bar";
import { SharedVariablesHint } from "@/components/shared-variables-hint";
import { VariablesEditor } from "@/components/variables-editor";
import { useServers } from "@/context/use-servers";
import { useCreateForm } from "@/hooks/use-create-form";
import { useRegistrations } from "@/hooks/use-registrations";
import { useSharedNames } from "@/hooks/use-shared-names";
import { pruneBlankAddresses } from "@/lib/app-editor-io";
import { hasErrorIn } from "@/lib/field-errors";
import { composeStart } from "@/lib/new-app";
import { appsPath } from "@/lib/routes";
import type { AppEditorState } from "@/types/app-config";

// ComposeFlow is New app → Compose file: a name (its tile follows it), the
// compose file, variables and one address; description, redirects and
// image versions under "More options". Create deploys it at once.
export function ComposeFlow({ serverId }: { serverId: string }) {
  const { activeServer, loading: serversLoading } = useServers();
  const ingressOn = Boolean(activeServer?.features?.ingress);
  const shared = useSharedNames(serverId);
  const { registrations, refresh } = useRegistrations(serverId, ingressOn);
  const [state, setState] = useState<AppEditorState>(composeStart);
  const [varsOpen, setVarsOpen] = useState(false);
  const [moreOpen, setMoreOpen] = useState(false);

  // ready is what Create sends: the untouched Address row dropped (and the
  // whole ingress on a server without the proxy).
  const ready = useMemo(() => {
    const pruned = pruneBlankAddresses(state);
    return ingressOn ? pruned : { ...pruned, config: { ...pruned.config, ingress: undefined } };
  }, [state, ingressOn]);
  const { form, errors, invalid, busy, error, submit } = useCreateForm(serverId, ready);
  const config = state.config;
  const setConfig = (patch: Partial<AppEditorState["config"]>) =>
    setState((s) => ({ ...s, config: { ...s.config, ...patch } }));
  const rows = { state, onChange: setState, serverId, registrations, onRefresh: refresh, errors };

  // A refused Create opens the sections holding a bad field. They are
  // opened for good (not only while the error lasts), so fixing the field
  // doesn't fold it away under the cursor.
  const create = () =>
    submit((found) => {
      if (hasErrorIn(found, "var:")) setVarsOpen(true);
      if (hasErrorIn(found, "redirect-")) setMoreOpen(true);
    });

  return (
    <div ref={form} className="space-y-8">
      <NameField
        value={{ name: config.name, icon: config.icon ?? "", color: config.color ?? "" }}
        onChange={(look) => setConfig(look)}
        error={errors.name}
      />

      <section aria-labelledby="compose-title" className="space-y-3">
        <h2 id="compose-title" className="text-[15px] font-semibold">
          Compose file
        </h2>
        <FieldMessage id="files" message={errors.files} className="text-sm" />
        <FileEditors
          files={config.files}
          contents={state.files}
          onChange={(files, contents) => setState((s) => ({ ...s, config: { ...s.config, files }, files: contents }))}
          imageChips={false}
          errors={errors}
          addLabel="Add another file"
        />
      </section>

      <Disclosure label={`Variables (${config.variables.length})`} open={varsOpen} onOpenChange={setVarsOpen}>
        <p className="text-sm text-muted-foreground">
          Used in the files as <code>{"${NAME}"}</code>. Secret values are encrypted in your browser.
        </p>
        <VariablesEditor state={state} onChange={setState} shared={shared} errors={errors} />
        <SharedVariablesHint shared={shared} />
      </Disclosure>

      {serversLoading ? null : ingressOn ? (
        <section aria-labelledby="address-title" className="space-y-3">
          <div className="space-y-1">
            <h2 id="address-title" className="text-[15px] font-semibold">
              Address
            </h2>
            <p className="text-sm text-muted-foreground">Where people reach the app. Leave it empty to keep the app private.</p>
          </div>
          <AddressRows {...rows} addLabel="Add another" emptyNote={null} />
        </section>
      ) : (
        <p className="text-sm text-muted-foreground">
          This server has no built-in web proxy: people reach the app on the port its compose file publishes.
        </p>
      )}

      <Disclosure open={moreOpen} onOpenChange={setMoreOpen}>
        <FormField id="app-desc" label="Description">
          <Input
            id="app-desc"
            value={config.description ?? ""}
            placeholder="Optional"
            onChange={(e) => setConfig({ description: e.target.value })}
          />
        </FormField>
        {ingressOn ? (
          <div className="space-y-2">
            <h3 className="text-sm font-medium">Redirects</h3>
            <RedirectRows {...rows} />
          </div>
        ) : null}
        <ImageVersions
          files={config.files}
          contents={state.files}
          onReplace={(fileId, oldRef, newRef) =>
            setState((s) => ({ ...s, files: { ...s.files, [fileId]: (s.files[fileId] ?? "").split(oldRef).join(newRef) } }))
          }
        />
      </Disclosure>

      <CreateBar error={error} busy={busy} onCreate={create} cancelTo={appsPath(serverId)} invalid={invalid} />
    </div>
  );
}
