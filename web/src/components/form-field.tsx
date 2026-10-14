import type { ReactNode } from "react";

import { Label } from "@/components/ui/label";
import { errorId } from "@/lib/field-errors";
import { cn } from "@/lib/utils";

// FormField is one labelled field: the label, the control (children, which
// spread invalidAttrs(id, error)), and under it the field's message, or
// its hint while the field is fine.
export function FormField({
  id,
  label,
  hint,
  error,
  className,
  children,
}: {
  id: string;
  label: ReactNode;
  hint?: ReactNode;
  error?: string;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className={cn("grid gap-1.5", className)}>
      <Label htmlFor={id}>{label}</Label>
      {children}
      {error ? (
        <FieldMessage id={id} message={error} />
      ) : hint ? (
        <p className="text-xs text-muted-foreground">{hint}</p>
      ) : null}
    </div>
  );
}

// FieldMessage is the message under the field with this id (nothing when
// the field is fine). It can take focus (tabIndex -1), so a refusal whose
// message has no control of its own (a missing compose file) can focus it.
export function FieldMessage({ id, message, className }: { id: string; message?: string; className?: string }) {
  if (!message) return null;
  return (
    <p id={errorId(id)} tabIndex={-1} data-refusal className={cn("text-xs text-destructive outline-none", className)}>
      {message}
    </p>
  );
}
