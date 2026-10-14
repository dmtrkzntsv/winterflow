import { useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
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
import { useUsedHostPorts } from "@/hooks/use-used-host-ports";
import { pruneBlankAddresses } from "@/lib/app-editor-io";
import { hasErrorIn } from "@/lib/field-errors";
import { undeclaredNames, withVariables } from "@/lib/file-variables";
import { firstFreeHostPort, randomHostPort } from "@/lib/host-ports";
import { composeStart, withHostPort } from "@/lib/new-app";
import { appsPath } from "@/lib/routes";
import type { AppEditorState } from "@/types/app-config";

// ComposeFlow is New app → Compose file: a name (its tile follows it), the
// compose file (with an offer to add the ${NAME}s it uses that nothing
// sets), variables and one address; description, redirects and image
// versions under "More options". Create deploys it at once.
export function ComposeFlow({ serverId }: { serverId: string }) {
  const { activeServer, loading: serversLoading } = useServers();
  const ingressOn = Boolean(activeServer?.features?.ingress);
  const shared = useSharedNames(serverId);
  const { registrations, refresh } = useRegistrations(serverId, ingressOn);
  // The example file publishes a random port of 8080-8999 until the ports
  // the server's apps publish are read, then the first free one (while the
  // file is untouched).
  const [startPort] = useState(randomHostPort);
  const [state, setState] = useState<AppEditorState>(() => composeStart(startPort));
  const used = useUsedHostPorts(serverId);
  const [portsSeen, setPortsSeen] = useState<ReadonlySet<number> | null>(null);
  if (used && used !== portsSeen) {
    setPortsSeen(used);
    const free = firstFreeHostPort(used);
    if (free !== undefined) setState((s) => withHostPort(s, startPort, free));
  }
  const [varsOpen, setVarsOpen] = useState(false);
  const [moreOpen, setMoreOpen] = useState(false);

  // ready is what Create sends: the untouched Address row dropped (and the
  // whole ingress on a server without the proxy).
  const ready = useMemo(() => {
    const pruned = pruneBlankAddresses(state);
    return ingressOn ? pruned : { ...pruned, config: { ...pruned.config, ingress: undefined } };
  }, [state, ingressOn]);
  const { form, errors, invalid, busy, error, stillCreating, held, submit } = useCreateForm(serverId, ready);
  const config = state.config;
  const setConfig = (patch: Partial<AppEditorState["config"]>) =>
    setState((s) => ({ ...s, config: { ...s.config, ...patch } }));
  const rows = { state, onChange: setState, serverId, registrations, onRefresh: refresh, errors };
  const undeclared = useMemo(() => undeclaredNames(state, shared), [state, shared]);
  const addUndeclared = () => {
    setState((s) => withVariables(s, undeclaredNames(s, shared)));
    setVarsOpen(true);
  };

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

      {undeclared.length > 0 ? (
        <div
          role="status"
          aria-label="Variables the files use"
          className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-lg border bg-muted/30 px-4 py-3 text-sm"
        >
          <span className="mr-auto min-w-0">
            Your files use {undeclared.length === 1 ? "a variable" : `${undeclared.length} variables`} nothing sets:{" "}
            {undeclared.map((n, i) => (
              <span key={n}>
                {i > 0 ? ", " : null}
                <code className="text-xs [overflow-wrap:anywhere]">{n}</code>
              </span>
            ))}
          </span>
          <Button size="sm" variant="outline" onClick={addUndeclared}>
            {undeclared.length === 1 ? "Add 1 variable" : `Add ${undeclared.length} variables`}
          </Button>
        </div>
      ) : null}

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
          People reach the app on the port its compose file publishes.
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

      <CreateBar
        error={error}
        busy={busy}
        onCreate={create}
        cancelTo={appsPath(serverId)}
        invalid={invalid}
        stillCreatingTo={stillCreating ? appsPath(serverId) : ""}
        held={held}
      />
    </div>
  );
}
