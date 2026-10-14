import { Disclosure } from "@/components/disclosure";
import type { SharedName } from "@/lib/shared-variables";

// SharedVariablesHint is the editor's read-only note of the shared
// variables an app can use as ${NAME} — never a value: the organization's
// and the server's named in one line, the built-in ones folded away behind
// "Built-in (N)".
export function SharedVariablesHint({ shared }: { shared: SharedName[] }) {
  const own = shared.filter((s) => s.scope !== "builtin");
  const builtin = shared.filter((s) => s.scope === "builtin");
  if (shared.length === 0) return null;
  return (
    <div className="space-y-1 text-sm text-muted-foreground">
      {own.length > 0 ? (
        <p>
          Shared variables you can use:{" "}
          {own.map((s, i) => (
            <span key={`${s.scope}:${s.name}`}>
              {i > 0 ? ", " : null}
              <code className="text-xs text-foreground">{s.name}</code>
            </span>
          ))}
        </p>
      ) : null}
      {builtin.length > 0 ? (
        <Disclosure label={`Built-in (${builtin.length})`}>
          <ul aria-label="Built-in variables" className="grid gap-1 sm:grid-cols-2">
            {builtin.map((s) => (
              <li key={s.name}>
                <code className="text-xs">{s.name}</code>
              </li>
            ))}
          </ul>
        </Disclosure>
      ) : null}
    </div>
  );
}
