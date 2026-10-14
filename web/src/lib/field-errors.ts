import { cn } from "@/lib/utils";

// FieldErrors are a form's problems by field key (see editorErrors for the
// app editor's keys). A key is present only with a message.
export type FieldErrors = Record<string, string>;

// errorId is the id of the message under the field with this id.
export function errorId(id: string): string {
  return `${id}-error`;
}

// invalidAttrs marks a field invalid (red border, described by its message)
// when message is set; the vendored inputs don't style aria-invalid
// themselves. Spread it last on the input: it carries the className.
export function invalidAttrs(id: string, message: string | undefined, className?: string) {
  return {
    "aria-invalid": message ? (true as const) : undefined,
    "aria-describedby": message ? errorId(id) : undefined,
    className: cn(className, message && "border-destructive focus-visible:border-destructive focus-visible:ring-destructive"),
  };
}

// hasErrorIn says whether any error's key equals or starts with one of
// prefixes (a disclosure holding those fields must open).
export function hasErrorIn(errors: FieldErrors, ...prefixes: string[]): boolean {
  return Object.keys(errors).some((k) => prefixes.some((p) => k === p || k.startsWith(p)));
}

// REFUSAL_ATTR marks a message that stands for a refusal with no field to
// focus (tabIndex -1 on it): focusFirstInvalid falls back to it.
export const REFUSAL_ATTR = "data-refusal";

// focusFirstInvalid moves focus to the first field marked invalid inside
// root (or, when none is, a message marked REFUSAL_ATTR), one frame later:
// after React painted the messages and opened any disclosure holding one.
export function focusFirstInvalid(root: HTMLElement | null): void {
  requestAnimationFrame(() => {
    const el =
      root?.querySelector<HTMLElement>('[aria-invalid="true"]') ??
      root?.querySelector<HTMLElement>(`[${REFUSAL_ATTR}]`);
    if (!el) return;
    el.focus();
    el.scrollIntoView?.({ block: "center" });
  });
}
