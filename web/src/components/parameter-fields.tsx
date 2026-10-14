import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { PasswordInput } from "@/components/ui/password-input";
import { cn } from "@/lib/utils";
import { fieldErrors, requiredFirst } from "@/lib/template-params";
import type { Parameter } from "@/lib/templates";

// nameOf is a field's accessible name: its description and parameter
// name, or the name alone.
const nameOf = (p: Parameter) => (p.description ? `${p.description} (${p.name})` : p.name);

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
// for the server before anything is sent). A field is labelled by the
// parameter's description when it has one (its name small beside it),
// else by its name; a secret carries a Secret tag.
export function ParameterFields({ params, values, onChange, kept = [], showRequired = false }: Props) {
  const errors = fieldErrors(params, values, kept);
  return (
    <div className="grid gap-4">
      {requiredFirst(params).map((p) => {
        const id = `param-${p.name}`;
        const err = errors[p.name] ?? "";
        const shown = err !== "" && (err !== "Required" || showRequired);
        const value = values[p.name] ?? "";
        // An invalid field gets the red border (the vendored inputs don't
        // style aria-invalid) and points at its message.
        const invalid = {
          "aria-invalid": shown || undefined,
          "aria-describedby": shown ? `${id}-error` : undefined,
          className: cn(shown && "border-destructive focus-visible:ring-destructive"),
        };
        return (
          <div key={p.name} className="grid gap-1.5">
            <Label htmlFor={id} className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
              <span className={cn(!p.description && "font-mono")}>
                {p.description || p.name}
                {p.required ? (
                  <span aria-hidden className="text-destructive">
                    *
                  </span>
                ) : null}
              </span>
              {p.secret ? (
                <Badge variant="secondary" className="px-1.5 py-0 text-[11px] font-normal">
                  Secret
                </Badge>
              ) : null}
              {p.description ? (
                <code className="ml-auto font-mono text-xs font-normal text-muted-foreground">{p.name}</code>
              ) : null}
            </Label>
            {p.secret ? (
              <PasswordInput
                id={id}
                aria-label={nameOf(p)}
                value={value}
                autoComplete="new-password"
                placeholder={kept.includes(p.name) ? "Stored, leave blank to keep" : "Encrypted in your browser"}
                {...invalid}
                onChange={(e) => onChange(p.name, e.target.value)}
              />
            ) : (
              <Input
                id={id}
                aria-label={nameOf(p)}
                value={value}
                placeholder={p.default ? `Default: ${p.default}` : undefined}
                {...invalid}
                className={cn("font-mono", invalid.className)}
                onChange={(e) => onChange(p.name, e.target.value)}
              />
            )}
            {shown ? (
              <p id={`${id}-error`} className="text-xs text-destructive">
                {err}
              </p>
            ) : null}
          </div>
        );
      })}
    </div>
  );
}
