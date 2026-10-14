import { useState } from "react";

import { ErrorText } from "@/components/error-text";
import { GitSourceFields } from "@/components/git-source-fields";
import { NameField } from "@/components/name-field";
import { CreateBar } from "@/components/new-app/create-bar";
import { useCreateForm } from "@/hooks/use-create-form";
import { newGitSource } from "@/lib/app-editor-io";
import { gitStart } from "@/lib/new-app";
import { appsPath } from "@/lib/routes";
import type { AppEditorState, AppSourceConfig } from "@/types/app-config";

// GitFlow is New app → Git repository: a name, the repository and branch;
// the token behind "Private repository?", the compose path and
// auto-redeploy under "Advanced". The app runs the repository's compose
// file; addresses and variables are added afterwards under Configuration.
export function GitFlow({ serverId }: { serverId: string }) {
  const [state, setState] = useState<AppEditorState>(gitStart);
  const { form, errors, invalid, busy, error, submit } = useCreateForm(serverId, state);
  const config = state.config;
  const source = config.source ?? newGitSource();

  const setSource = (patch: Partial<AppSourceConfig>) =>
    setState((s) => ({ ...s, config: { ...s.config, source: { ...(s.config.source ?? newGitSource()), ...patch } } }));

  return (
    <div ref={form} className="space-y-6">
      <NameField
        value={{ name: config.name, icon: config.icon ?? "", color: config.color ?? "" }}
        onChange={(look) => setState((s) => ({ ...s, config: { ...s.config, ...look } }))}
        error={errors.name}
      />
      <GitSourceFields
        source={source}
        token={state.sourceToken ?? ""}
        onSource={setSource}
        onToken={(sourceToken) => setState((s) => ({ ...s, sourceToken }))}
        errors={errors}
      />
      <p className="text-sm text-muted-foreground">
        The app runs the repository's compose file. Add addresses and variables under Configuration once it is created.
      </p>
      {error ? <ErrorText alert icon text={error} /> : null}
      <CreateBar busy={busy} onCreate={() => submit()} cancelTo={appsPath(serverId)} invalid={invalid} />
    </div>
  );
}
