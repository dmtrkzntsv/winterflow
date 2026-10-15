import { useId } from "react";

import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { PasswordInput } from "@/components/ui/password-input";
import { FieldMessage } from "@/components/form-field";
import { declaredPatternHint } from "@/lib/declared-variables";
import { invalidAttrs } from "@/lib/field-errors";
import { requiredFirst, variableErrors } from "@/lib/template-values";
import { cn } from "@/lib/utils";
import type { DeclaredVariable } from "@/types/app-config";

// nameOf is a field's accessible name: its description and variable name,
// or the name alone.
const nameOf = (v: DeclaredVariable) => (v.description ? `${v.description} (${v.name})` : v.name);

const REQUIRED = "Enter a value: this variable is required.";

type Props = {
  vars: DeclaredVariable[] | null | undefined;
  values: Record<string, string>; // plain values and typed secrets by name
  onChange: (name: string, value: string) => void;
  // kept are the secrets the agent holds: blank keeps them.
  kept?: readonly string[];
  // showRequired reveals "required" (after a submit attempt); a $ in a
  // plain value shows as it is typed.
  showRequired?: boolean;
};

// VariableFields is a form over the variables a template's compose files
// declare under x-winterflow: required ones first, the compose file's own
// value as the placeholder (a field left empty keeps it), secrets masked
// (the caller encrypts them for the server before anything is sent). A
// field is labelled by the variable's description when it has one (its name
// small beside it), else by its name; a secret carries a Secret tag. A
// value that may not match the pattern gets a hint: the server decides.
export function VariableFields({ vars, values, onChange, kept = [], showRequired = false }: Props) {
  // Field ids are unique per form: the upgrade dialog's fields open over
  // the instance panel's, with the same variables.
  const base = useId();
  const errors = variableErrors(vars, values, kept);
  return (
    <div className="grid gap-4">
      {requiredFirst(vars).map((v) => {
        const id = `${base}-var-${v.name}`;
        const err = errors[v.name] ?? "";
        const shown = err !== "" && (err !== REQUIRED || showRequired) ? err : undefined;
        const value = values[v.name] ?? "";
        const held = v.encrypted && kept.includes(v.name);
        const hint = shown ? "" : declaredPatternHint(v, value, held);
        // A phone gets the 40px touch height (the masked input is 36px).
        const attrs = invalidAttrs(id, shown, v.encrypted ? "max-sm:h-10" : "font-mono");
        return (
          <div key={v.name} className="grid gap-1.5">
            <Label htmlFor={id} className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
              <span className={cn(!v.description && "font-mono")}>
                {v.description || v.name}
                {v.required ? (
                  <span aria-hidden className="text-destructive">
                    *
                  </span>
                ) : null}
              </span>
              {v.encrypted ? (
                <Badge variant="secondary" className="px-1.5 py-0 text-[11px] font-normal">
                  Secret
                </Badge>
              ) : null}
              {v.description ? (
                <code className="ml-auto font-mono text-xs font-normal text-muted-foreground">{v.name}</code>
              ) : null}
            </Label>
            {v.encrypted ? (
              <PasswordInput
                id={id}
                aria-label={nameOf(v)}
                value={value}
                autoComplete="new-password"
                placeholder={held ? "Stored, leave blank to keep" : "Encrypted in your browser"}
                {...attrs}
                onChange={(e) => onChange(v.name, e.target.value)}
              />
            ) : (
              <Input
                id={id}
                aria-label={nameOf(v)}
                value={value}
                placeholder={v.default ? `Default: ${v.default}` : undefined}
                {...attrs}
                onChange={(e) => onChange(v.name, e.target.value)}
              />
            )}
            <FieldMessage id={id} message={shown} />
            {hint ? <p className="text-xs text-muted-foreground">{hint}</p> : null}
          </div>
        );
      })}
    </div>
  );
}
