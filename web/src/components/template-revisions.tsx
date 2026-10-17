import { useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { InlineError } from "@/components/inline-error";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { diffLines, hunks } from "@/lib/line-diff";
import { catalogComposeExample } from "@/lib/catalog-compose";
import type { TemplateBody, TemplateView } from "@/lib/templates";
import { cn, relativeTime } from "@/lib/utils";

// SETTINGS names the pseudo-file holding everything but the files.
const SETTINGS = "template settings";

// revisionTexts are a body's comparable texts: each file, and its other
// fields as indented JSON.
function revisionTexts(body: TemplateBody, catalog: boolean): Record<string, string> {
  const { files, ...rest } = body;
  const shownFiles = Object.fromEntries(Object.entries(files ?? {}).map(([name, content]) => [
    name, catalog && /\.ya?ml$/i.test(name) ? catalogComposeExample(content) : content,
  ]));
  return { ...shownFiles, [SETTINGS]: JSON.stringify(rest, null, 2) };
}

type FileDiff = { name: string; status: "added" | "removed" | "changed"; hunks: ReturnType<typeof hunks> };

function diffBodies(before: TemplateBody, after: TemplateBody, catalog: boolean): FileDiff[] {
  const a = revisionTexts(before, catalog);
  const b = revisionTexts(after, catalog);
  const names = [...new Set([...Object.keys(a), ...Object.keys(b)])].sort();
  const out: FileDiff[] = [];
  for (const name of names) {
    if (a[name] === b[name]) continue;
    const h = hunks(diffLines(a[name] ?? "", b[name] ?? ""));
    if (h.length === 0) continue;
    out.push({ name, status: !(name in a) ? "added" : !(name in b) ? "removed" : "changed", hunks: h });
  }
  return out;
}

// TemplateRevisions is the Versions tab: a template's versions, newest
// first; each opens its changes against the version before it.
export function TemplateRevisions({ view }: { view: TemplateView }) {
  const api = useTemplatesApi();
  const [open, setOpen] = useState<number | null>(null);
  // diffs keeps the loaded changes by revision; failed is the last failed
  // load (not kept: opening it again retries).
  const [diffs, setDiffs] = useState<Record<number, FileDiff[]>>({});
  const [failed, setFailed] = useState<{ revision: number; message: string } | null>(null);

  const toggle = async (revision: number) => {
    if (open === revision) {
      setOpen(null);
      return;
    }
    setOpen(revision);
    if (diffs[revision]) return;
    setFailed((f) => (f?.revision === revision ? null : f));
    try {
      const [prev, cur] = await Promise.all([api.getRevision(view.id, revision - 1), api.getRevision(view.id, revision)]);
      setDiffs((d) => ({ ...d, [revision]: diffBodies(prev.body, cur.body, view.source === "catalog") }));
    } catch (e) {
      setFailed({ revision, message: e instanceof Error ? e.message : "Failed to load the versions" });
    }
  };

  return (
    <ol className="divide-y rounded-md border">
      {(view.revisions ?? []).map((r) => {
        const diff = diffs[r.revision];
        const error = failed?.revision === r.revision ? failed.message : null;
        return (
          <li key={r.revision} className="px-4 py-3 text-sm">
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <span className="font-medium">Version {r.revision}</span>
              {/* A version without a note says nothing more. */}
              <span className="min-w-0 flex-1 truncate text-muted-foreground">{r.note}</span>
              <time className="text-xs text-muted-foreground" dateTime={r.created_at}>
                {relativeTime(new Date(r.created_at))}
              </time>
              {r.revision > 1 ? (
                <Button size="sm" variant="ghost" onClick={() => void toggle(r.revision)}>
                  {open === r.revision ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />}
                  Changes from version {r.revision - 1}
                </Button>
              ) : (
                <span className="text-xs text-muted-foreground">First version</span>
              )}
            </div>
            {open === r.revision ? (
              <div className="mt-3 space-y-3">
                {error !== null ? (
                  <InlineError>{error}</InlineError>
                ) : diff === undefined ? (
                  <Spinner />
                ) : diff.length === 0 ? (
                  <p className="text-muted-foreground">No changes.</p>
                ) : (
                  diff.map((f) => (
                    <section key={f.name} aria-label={`Changes to ${f.name}`} className="overflow-hidden rounded-md border">
                      <h3 className="border-b bg-muted/40 px-3 py-1.5 font-mono text-xs">
                        {f.name}
                        {f.status !== "changed" ? <span className="ml-2 font-sans text-muted-foreground">{f.status}</span> : null}
                      </h3>
                      <pre className="overflow-x-auto font-mono text-xs leading-5">
                        {f.hunks.map((h, i) => (
                          <div key={i}>
                            {h.skipped > 0 ? (
                              <div className="bg-muted/30 px-3 text-muted-foreground">… {h.skipped} unchanged</div>
                            ) : null}
                            {h.lines.map((l, j) => (
                              <div
                                key={j}
                                className={cn(
                                  "px-3",
                                  l.op === "+" && "bg-emerald-500/10 text-emerald-800 dark:text-emerald-300",
                                  l.op === "-" && "bg-red-500/10 text-red-800 dark:text-red-300",
                                )}
                              >
                                {l.op} {l.text}
                              </div>
                            ))}
                          </div>
                        ))}
                      </pre>
                    </section>
                  ))
                )}
              </div>
            ) : null}
          </li>
        );
      })}
    </ol>
  );
}
