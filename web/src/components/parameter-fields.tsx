import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { PasswordInput } from "@/components/ui/password-input";
import { fieldErrors, requiredFirst } from "@/lib/template-params";
import type { Parameter } from "@/lib/templates";

type Props = {
  params: Parameter[] | null | undefined;
  values: Record<string, string>; // plain values and typed secrets by name
  onChange: (name: string, value: string) => void;
  // kept are the secrets the agent holds: blank keeps them.
  kept?: readonly string[];
  // showRequired reveals "Required" (after a submit attempt); a value that
  // breaks its pattern shows as it is typed.
  showRequired?: boolean;
};

// ParameterFields is a form over a template's parameters: required ones
// first, each checked as typed, secrets masked (the caller encrypts them
// for the server before anything is sent).
export function ParameterFields({ params, values, onChange, kept = [], showRequired = false }: Props) {
  const errors = fieldErrors(params, values, kept);
  return (
    <div className="grid gap-4">
      {requiredFirst(params).map((p) => {
        const id = `param-${p.name}`;
        const err = errors[p.name] ?? "";
        const shown = err !== "" && (err !== "Required" || showRequired);
        const value = values[p.name] ?? "";
        return (
          <div key={p.name} className="grid gap-1.5">
            <Label htmlFor={id} className="font-mono">
              {p.name}
              {p.required ? (
                <span aria-hidden className="text-destructive">
                  *
                </span>
              ) : null}
              {p.secret ? <span className="font-sans text-xs font-normal text-muted-foreground">secret</span> : null}
            </Label>
            {p.secret ? (
              <PasswordInput
                id={id}
                aria-label={p.name}
                value={value}
                autoComplete="new-password"
                placeholder={kept.includes(p.name) ? "Stored, leave blank to keep" : "Encrypted in your browser"}
                aria-invalid={shown || undefined}
                onChange={(e) => onChange(p.name, e.target.value)}
              />
            ) : (
              <Input
                id={id}
                aria-label={p.name}
                value={value}
                className="font-mono"
                placeholder={p.default ? `Default: ${p.default}` : undefined}
                aria-invalid={shown || undefined}
                onChange={(e) => onChange(p.name, e.target.value)}
              />
            )}
            {p.description ? <p className="text-xs text-muted-foreground">{p.description}</p> : null}
            {shown ? <p className="text-xs text-destructive">{err}</p> : null}
          </div>
        );
      })}
    </div>
  );
}
