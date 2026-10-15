import { Lock } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FieldMessage } from "@/components/form-field";
import { holdsStoredSecret } from "@/lib/app-editor-io";
import { declaredPatternHint, declaredRow, declaredValueError, setDeclaredValue } from "@/lib/declared-variables";
import { ENCRYPTED } from "@/lib/ecies";
import { errorId, invalidAttrs, type FieldErrors } from "@/lib/field-errors";
import type { AppEditorState } from "@/types/app-config";

type Props = {
  state: AppEditorState;
  onChange: (next: AppEditorState) => void;
  // errors holds decl:<NAME> messages (editorErrors), shown after a refused
  // save; a value that breaks its rule is flagged as it is typed anyway.
  errors?: FieldErrors;
};

// DeclaredVariablesForm edits the variables the compose files declare under
// x-winterflow: one field each, labelled with its name and description,
// the compose file's own value as the placeholder (left empty, it stands),
// a required marker, and the pattern checked as you type. A stored secret
// reads "Secret · set" with Replace, like the free-form rows.
export function DeclaredVariablesForm({ state, onChange, errors = {} }: Props) {
  const declared = state.declared ?? [];
  if (declared.length === 0) return null;
  return (
    <div className="space-y-4" role="group" aria-label="Declared variables">
      {declared.map((v) => {
        const row = declaredRow(state, v);
        const value = row ? (state.variables[row.id] ?? "") : "";
        const held = row !== undefined && holdsStoredSecret(row, row.name);
        const id = `decl-${v.name}`;
        const live = value === "" ? "" : declaredValueError(v, value, held);
        const message = errors[`decl:${v.name}`] || live;
        const hint = message ? "" : declaredPatternHint(v, value, held);
        const hintId = `${id}-hint`;
        return (
          <div key={v.name} className="space-y-1.5">
            <label htmlFor={id} className="flex items-center gap-1.5 text-sm font-medium">
              <code className="font-mono">{v.name}</code>
              {v.required ? (
                <span className="text-destructive" aria-label="required">
                  *
                </span>
              ) : null}
              {v.encrypted ? <Lock className="size-3 text-muted-foreground" aria-label="secret" /> : null}
            </label>
            {v.description ? <p className="text-xs text-muted-foreground">{v.description}</p> : null}
            {held && value === ENCRYPTED ? (
              <div className="flex items-center gap-2 text-sm">
                <span className="text-muted-foreground">Secret · set</span>
                <Button size="sm" variant="outline" className="h-7" aria-label={`Replace ${v.name}`} onClick={() => onChange(setDeclaredValue(state, v, ""))}>
                  Replace
                </Button>
              </div>
            ) : (
              <Input
                id={id}
                value={value}
                type={v.encrypted ? "password" : "text"}
                autoComplete="off"
                placeholder={held ? "Leave empty to keep the current value" : (v.default ?? "")}
                {...invalidAttrs(id, message, "font-mono")}
                aria-describedby={message ? errorId(id) : hint ? hintId : undefined}
                onChange={(e) => onChange(setDeclaredValue(state, v, e.target.value))}
              />
            )}
            <FieldMessage id={id} message={message} />
            {hint ? (
              <p id={hintId} className="text-xs text-muted-foreground">
                {hint}
              </p>
            ) : null}
          </div>
        );
      })}
    </div>
  );
}
