import { useState } from "react";
import { Eye, EyeOff, Plus, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SecretToggle } from "@/components/files-section";
import { FieldMessage } from "@/components/form-field";
import { EmptyNote } from "@/components/page-states";
import { addVariable, removeVariable } from "@/lib/editor-rows";
import { invalidAttrs, type FieldErrors } from "@/lib/field-errors";
import { overrideNote, type SharedName } from "@/lib/shared-variables";
import type { AppEditorState, AppVariableMeta } from "@/types/app-config";

type Props = {
  state: AppEditorState;
  onChange: (next: AppEditorState) => void;
  // shared are the org/server variables an app variable may override.
  shared: SharedName[];
  // errors holds var:<id> messages (editorErrors).
  errors?: FieldErrors;
  // emptyNote says "No variables yet." when there are none.
  emptyNote?: boolean;
};

// VariablesEditor edits an app's variables: name, value (masked while
// secret), the secret toggle and removal, then "Add variable". Secret values
// are encrypted by the save, never here.
export function VariablesEditor({ state, onChange, shared, errors = {}, emptyNote = true }: Props) {
  const [revealed, setRevealed] = useState<Record<string, boolean>>({});
  const vars = state.config.variables;
  const updateMeta = (id: string, patch: Partial<AppVariableMeta>) =>
    onChange({
      ...state,
      config: { ...state.config, variables: vars.map((v) => (v.id === id ? { ...v, ...patch } : v)) },
    });
  const updateValue = (id: string, value: string) => onChange({ ...state, variables: { ...state.variables, [id]: value } });

  return (
    <div className="space-y-3">
      {vars.length === 0 ? (
        emptyNote ? (
          <EmptyNote>No variables yet.</EmptyNote>
        ) : null
      ) : (
        <div className="divide-y rounded-md border">
          {vars.map((v) => {
            const isRevealed = revealed[v.id] || !v.is_encrypted;
            const note = overrideNote(v.name, shared);
            const nameId = `var-${v.id}`;
            const error = errors[`var:${v.id}`];
            return (
              <div key={v.id} className="p-1">
                <div className="flex items-center gap-1">
                  <Input
                    id={nameId}
                    aria-label="Variable name"
                    value={v.name}
                    placeholder="VAR_NAME"
                    {...invalidAttrs(
                      nameId,
                      error,
                      "h-8 w-32 shrink-0 border-transparent font-mono shadow-none focus-visible:border-input sm:w-56",
                    )}
                    onChange={(e) => updateMeta(v.id, { name: e.target.value })}
                  />
                  {note ? <span className="shrink-0 text-xs text-muted-foreground">{note}</span> : null}
                  <Input
                    aria-label={`Value of ${v.name || "variable"}`}
                    value={state.variables[v.id] ?? ""}
                    type={isRevealed ? "text" : "password"}
                    placeholder="value"
                    className="h-8 border-transparent font-mono shadow-none focus-visible:border-input"
                    onChange={(e) => updateValue(v.id, e.target.value)}
                  />
                  {v.is_encrypted ? (
                    <Button
                      size="icon"
                      variant="ghost"
                      className="size-8 shrink-0 text-muted-foreground"
                      aria-label={isRevealed ? "Hide value" : "Show value"}
                      onClick={() => setRevealed((r) => ({ ...r, [v.id]: !r[v.id] }))}
                    >
                      {isRevealed ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                    </Button>
                  ) : null}
                  <SecretToggle on={v.is_encrypted} onChange={(on) => updateMeta(v.id, { is_encrypted: on })} />
                  <Button
                    size="icon"
                    variant="ghost"
                    className="size-8 shrink-0 text-muted-foreground hover:text-destructive"
                    aria-label={`Remove ${v.name || "variable"}`}
                    onClick={() => onChange(removeVariable(state, v.id))}
                  >
                    <Trash2 className="size-4" />
                  </Button>
                </div>
                <FieldMessage id={nameId} message={error} className="px-2 pt-1 pb-1" />
              </div>
            );
          })}
        </div>
      )}
      <Button size="sm" variant="outline" onClick={() => onChange(addVariable(state))}>
        <Plus className="size-4" /> Add variable
      </Button>
    </div>
  );
}
