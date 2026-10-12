import { Badge } from "@/components/ui/badge";
import type { SharedName } from "@/lib/shared-variables";

const SCOPE_LABEL: Record<SharedName["scope"], string> = { org: "org", server: "server", builtin: "built-in" };

// SharedVariablesHint is the editor's collapsed, read-only list of shared
// variables an app can use as ${NAME} — never a value.
export function SharedVariablesHint({ shared }: { shared: SharedName[] }) {
  if (shared.length === 0) return null;
  return (
    <details className="rounded-md border p-3 text-sm">
      <summary className="cursor-pointer text-muted-foreground">
        Shared variables available to this app ({shared.length})
      </summary>
      <ul className="mt-2 grid gap-1 sm:grid-cols-2">
        {shared.map((s) => (
          <li key={`${s.scope}:${s.name}`} className="flex items-center gap-2">
            <code className="text-xs">{s.name}</code>
            <Badge variant="outline">{SCOPE_LABEL[s.scope]}</Badge>
          </li>
        ))}
      </ul>
    </details>
  );
}
