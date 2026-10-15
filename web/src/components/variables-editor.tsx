import { useRef, useState } from "react";
import { Eye, EyeOff, Lock, Plus, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SecretToggle } from "@/components/files-section";
import { FieldMessage } from "@/components/form-field";
import { EmptyNote } from "@/components/page-states";
import { holdsStoredSecret } from "@/lib/app-editor-io";
import { ENCRYPTED } from "@/lib/ecies";
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
  // hidden are the ids of rows another part of the editor edits (the
  // declared variables' form): they are not listed here. By id, never by
  // name: a row whose name is typed to match a hidden one stays.
  hidden?: ReadonlySet<string>;
};

// VariablesEditor edits an app's variables: name, value (masked while
// secret), the secret toggle and removal, then "Add variable". A stored
// secret is never shown, nor its placeholder: it reads "Secret · set" with
// Replace, which opens an empty field (left empty, or Keep current, keeps
// the stored value). Secret values are encrypted by the save, never here.
export function VariablesEditor({ state, onChange, shared, errors = {}, emptyNote = true, hidden }: Props) {
  const [revealed, setRevealed] = useState<Record<string, boolean>>({});
  // replaced: the row whose new-value field Replace just opened (focused).
  const replaced = useRef<string | null>(null);
  const vars = state.config.variables;
  const shown = hidden ? vars.filter((v) => !hidden.has(v.id)) : vars;
  // updateMeta changes a row's name; renamed away from the name the agent
  // holds its secret under, a stored secret starts empty and needs a new
  // value (renamed back while still empty, it keeps the stored one).
  const updateMeta = (id: string, patch: Partial<AppVariableMeta>) => {
    const variables = vars.map((v) => (v.id === id ? { ...v, ...patch } : v));
    const changed = variables.find((v) => v.id === id);
    const drop = changed && state.variables[id] === ENCRYPTED && !holdsStoredSecret(changed, changed.name);
    onChange({
      ...state,
      config: { ...state.config, variables },
      variables: drop ? { ...state.variables, [id]: "" } : state.variables,
    });
  };
  const updateValue = (id: string, value: string) => onChange({ ...state, variables: { ...state.variables, [id]: value } });
  // setSecret flips a row's secret flag; a stored secret turned plain
  // starts empty (its value never comes back as text).
  const setSecret = (v: AppVariableMeta, on: boolean) => {
    const variables = vars.map((x) => (x.id === v.id ? { ...x, is_encrypted: on } : x));
    const value = state.variables[v.id] ?? "";
    const values = !on && value === ENCRYPTED ? { ...state.variables, [v.id]: "" } : state.variables;
    onChange({ ...state, config: { ...state.config, variables }, variables: values });
  };

  return (
    <div className="space-y-3">
      {shown.length === 0 ? (
        emptyNote ? (
          <EmptyNote>No variables yet.</EmptyNote>
        ) : null
      ) : (
        <div className="divide-y rounded-md border">
          {shown.map((v) => {
            const value = state.variables[v.id] ?? "";
            const held = holdsStoredSecret(v, v.name);
            const kept = held && value === ENCRYPTED;
            const isRevealed = revealed[v.id] || !v.is_encrypted;
            const valueId = `var-value-${v.id}`;
            const valueError = errors[`var:${v.id}:value`];
            const note = overrideNote(v.name, shared);
            const nameId = `var-${v.id}`;
            const error = errors[`var:${v.id}`];
            return (
              <div key={v.id} className="p-1">
                {/* On a phone the name takes the first line, whole. */}
                <div className="flex flex-wrap items-center gap-1 sm:flex-nowrap">
                  <Input
                    id={nameId}
                    aria-label="Variable name"
                    value={v.name}
                    placeholder="VAR_NAME"
                    {...invalidAttrs(
                      nameId,
                      error,
                      "h-8 w-full shrink-0 border-transparent font-mono shadow-none focus-visible:border-input sm:w-56",
                    )}
                    onChange={(e) => updateMeta(v.id, { name: e.target.value })}
                  />
                  {note ? <span className="shrink-0 text-xs text-muted-foreground">{note}</span> : null}
                  {kept ? (
                    <span className="flex min-w-0 flex-1 items-center gap-2 px-2 text-sm">
                      <span className="inline-flex min-w-0 items-center gap-1.5 text-muted-foreground">
                        <Lock className="size-3.5 shrink-0" aria-hidden />
                        <span className="truncate">Secret · set</span>
                      </span>
                      <Button
                        size="sm"
                        variant="outline"
                        className="ml-auto h-7"
                        aria-label={`Replace ${v.name || "secret"}`}
                        onClick={() => {
                          replaced.current = v.id;
                          setRevealed((r) => ({ ...r, [v.id]: false }));
                          updateValue(v.id, "");
                        }}
                      >
                        Replace
                      </Button>
                    </span>
                  ) : (
                    <>
                      <Input
                        id={valueId}
                        aria-label={`Value of ${v.name || "variable"}`}
                        value={value}
                        type={isRevealed ? "text" : "password"}
                        placeholder={held ? "Leave empty to keep the current value" : "value"}
                        autoComplete="off"
                        ref={(el) => {
                          if (el && replaced.current === v.id) {
                            replaced.current = null;
                            el.focus();
                          }
                        }}
                        {...invalidAttrs(valueId, valueError, "h-8 min-w-0 flex-1 border-transparent font-mono shadow-none focus-visible:border-input")}
                        onChange={(e) => updateValue(v.id, e.target.value)}
                      />
                      {v.is_encrypted && value !== "" ? (
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
                      {held ? (
                        <Button size="sm" variant="ghost" className="h-8 shrink-0 text-xs" onClick={() => updateValue(v.id, ENCRYPTED)}>
                          Keep current
                        </Button>
                      ) : null}
                    </>
                  )}
                  <SecretToggle on={v.is_encrypted} onChange={(on) => setSecret(v, on)} />
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
                <FieldMessage id={valueId} message={valueError} className="px-2 pt-1 pb-1" />
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
