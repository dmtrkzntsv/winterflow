import { Lock } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { EditorSection } from "@/components/editor-section";
import { InlineError } from "@/components/inline-error";
import { EmptyNote } from "@/components/page-states";
import type { Detected } from "@/lib/templates";

type Props = {
  detected: Detected | null;
  // problem is the first thing a save would refuse ("" when none).
  problem?: string;
  // error says the files could not be read just now.
  error?: string;
  checking?: boolean;
};

// TemplateDetected is the template editor's read-only Detected panel: what
// the compose files declare under x-winterflow, as the API reads it — the
// variables each app fills in, the ports it exposes (an http one takes
// hostnames), the backups it can turn on — and what a save would refuse or
// point out. It changes only through the files.
export function TemplateDetected({ detected, problem = "", error = "", checking = false }: Props) {
  const variables = detected?.variables ?? [];
  const expose = detected?.expose ?? [];
  const backups = detected?.backups ?? [];
  const warnings = detected?.warnings ?? [];
  const empty = variables.length === 0 && expose.length === 0 && backups.length === 0;
  return (
    <EditorSection
      title="Detected"
      description={
        <>
          Read from the compose file's <code>x-winterflow</code> block. Edit the files to change it.
          {checking ? <span className="block pt-1 text-xs">Checking…</span> : null}
        </>
      }
    >
      {problem ? <InlineError icon>A save would be refused: {problem}</InlineError> : null}
      {error ? <InlineError icon>Couldn't read the files: {error}</InlineError> : null}
      {detected === null ? null : empty ? (
        <EmptyNote>No variables, exposed ports or backups: each app takes no values and publishes nothing.</EmptyNote>
      ) : (
        <div className="grid gap-4 text-sm">
          {variables.length > 0 ? (
            <section aria-label="Variables" className="space-y-1.5">
              <h3 className="text-xs font-medium text-muted-foreground">Variables each app fills in</h3>
              <ul className="divide-y rounded-md border">
                {variables.map((v) => (
                  <li key={v.name} className="space-y-1 px-3 py-2">
                    <div className="flex flex-wrap items-center gap-2">
                      <code className="font-mono">{v.name}</code>
                      {v.required ? <Badge variant="secondary">Required</Badge> : null}
                      {v.encrypted ? (
                        <Badge variant="secondary">
                          <Lock className="size-3" aria-hidden /> Secret
                        </Badge>
                      ) : null}
                      <span className="ml-auto text-xs text-muted-foreground">
                        {v.encrypted ? "no default" : v.default !== null ? `default ${v.default || '""'}` : "no default"}
                      </span>
                    </div>
                    {v.description ? <p className="text-xs text-muted-foreground">{v.description}</p> : null}
                    {v.pattern ? (
                      <p className="text-xs text-muted-foreground">
                        Must match <code className="font-mono">{v.pattern}</code>
                      </p>
                    ) : null}
                  </li>
                ))}
              </ul>
            </section>
          ) : null}
          {expose.length > 0 ? (
            <section aria-label="Exposed ports" className="space-y-1.5">
              <h3 className="text-xs font-medium text-muted-foreground">Exposed ports (each app gets a host port of its own)</h3>
              <ul className="divide-y rounded-md border">
                {expose.map((e) => (
                  <li key={e.id} className="flex flex-wrap items-center gap-2 px-3 py-2">
                    <code className="font-mono">{e.target}</code>
                    <Badge variant="secondary">{e.protocol}</Badge>
                    <span className="ml-auto text-xs text-muted-foreground">
                      {e.protocol === "http" ? "takes hostnames" : "reachable on the LAN"}
                    </span>
                  </li>
                ))}
              </ul>
            </section>
          ) : null}
          {backups.length > 0 ? (
            <section aria-label="Backups" className="space-y-1.5">
              <h3 className="text-xs font-medium text-muted-foreground">Backups each app can turn on</h3>
              <ul className="divide-y rounded-md border">
                {backups.map((b) => (
                  <li key={b.name} className="flex items-center gap-2 px-3 py-2">
                    <code className="font-mono">{b.name}</code>
                    <span className="text-xs text-muted-foreground">{b.type}</span>
                  </li>
                ))}
              </ul>
            </section>
          ) : null}
        </div>
      )}
      {warnings.length > 0 ? (
        <ul aria-label="Warnings" className="list-disc space-y-1 pl-4 text-xs text-amber-800 dark:text-amber-300">
          {warnings.map((w) => (
            <li key={w}>{w}</li>
          ))}
        </ul>
      ) : null}
    </EditorSection>
  );
}
