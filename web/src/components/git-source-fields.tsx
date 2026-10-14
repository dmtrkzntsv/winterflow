import { useState } from "react";

import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { PasswordInput } from "@/components/ui/password-input";
import { Disclosure } from "@/components/disclosure";
import { FormField } from "@/components/form-field";
import { invalidAttrs, type FieldErrors } from "@/lib/field-errors";
import type { AppSourceConfig } from "@/types/app-config";

type Props = {
  source: AppSourceConfig;
  // token is a new access token typed this session ("" keeps the stored one).
  token: string;
  onSource: (patch: Partial<AppSourceConfig>) => void;
  onToken: (token: string) => void;
  // errors holds repo_url and branch messages (editorErrors).
  errors?: FieldErrors;
};

// GitSourceFields are where a Git-deployed app comes from: the repository
// and branch up front, the token behind "Private repository?", the compose
// file path and auto-redeploy under "Advanced". The token is encrypted for
// the server by the save, never here.
export function GitSourceFields({ source, token, onSource, onToken, errors = {} }: Props) {
  const [privateOpen, setPrivateOpen] = useState(Boolean(source.token_set) || token !== "");
  return (
    <div className="grid gap-4">
      <FormField id="src-url" label="Repository URL" error={errors.repo_url}>
        <Input
          id="src-url"
          value={source.repo_url}
          placeholder="https://github.com/org/app"
          autoComplete="off"
          {...invalidAttrs("src-url", errors.repo_url, "font-mono")}
          onChange={(e) => onSource({ repo_url: e.target.value })}
        />
      </FormField>
      <FormField id="src-branch" label="Branch" error={errors.branch} className="sm:max-w-xs">
        <Input
          id="src-branch"
          value={source.branch}
          placeholder="main"
          autoComplete="off"
          {...invalidAttrs("src-branch", errors.branch, "font-mono")}
          onChange={(e) => onSource({ branch: e.target.value })}
        />
      </FormField>
      <Disclosure label="Private repository?" open={privateOpen} onOpenChange={setPrivateOpen}>
        <FormField
          id="src-token"
          label="Access token"
          hint="A token that can read the repository. It is encrypted in your browser for this server."
        >
          <PasswordInput
            id="src-token"
            value={token}
            autoComplete="new-password"
            placeholder={source.token_set ? "Stored, leave blank to keep" : "Paste a token"}
            onChange={(e) => onToken(e.target.value)}
          />
        </FormField>
      </Disclosure>
      <Disclosure label="Advanced">
        <FormField
          id="src-compose"
          label="Compose file path"
          hint="Inside the repository. Empty uses the compose file at its root."
        >
          <Input
            id="src-compose"
            value={source.compose_path || ""}
            placeholder="compose.yml"
            className="font-mono"
            onChange={(e) => onSource({ compose_path: e.target.value })}
          />
        </FormField>
        <label className="flex min-h-10 items-center gap-2 text-sm">
          <Checkbox checked={source.auto_update} onCheckedChange={(c) => onSource({ auto_update: c === true })} />
          Redeploy automatically when the branch gets new commits
        </label>
      </Disclosure>
    </div>
  );
}
