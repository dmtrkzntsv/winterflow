import { useEffect, useState } from "react";

import type { Inspection, TemplateBody, TemplatesApi } from "@/lib/templates";

export type TemplateInspection = {
  // inspection is the latest answer (for an earlier body while checking).
  inspection: Inspection | null;
  // error is why the latest body could not be read ("" when it was).
  error: string;
  // checking: the body changed since the latest answer.
  checking: boolean;
};

type Answer = { key: string; inspection: Inspection | null; error: string };

// useTemplateInspection reads what a template body declares
// (inspect-template) delay ms after it last changed, while enabled: the
// editor's Detected panel follows the files without a YAML parser in the
// browser. An answer is kept with the body it is for, so a late one for an
// older body never reads as current; the panel keeps the last good
// inspection while the next is read.
export function useTemplateInspection(
  api: Pick<TemplatesApi, "inspectTemplate">,
  body: TemplateBody,
  enabled: boolean,
  delay = 500,
): TemplateInspection {
  const key = enabled ? JSON.stringify(body) : "";
  const [answer, setAnswer] = useState<Answer | null>(null);

  useEffect(() => {
    if (!key) return;
    let live = true;
    const timer = setTimeout(() => {
      api.inspectTemplate(JSON.parse(key) as TemplateBody).then(
        (inspection) => live && setAnswer({ key, inspection, error: "" }),
        (e: unknown) =>
          live &&
          setAnswer((prev) => ({
            key,
            inspection: prev?.inspection ?? null,
            error: e instanceof Error ? e.message : "The compose file could not be read",
          })),
      );
    }, delay);
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [api, key, delay]);

  const fresh = answer?.key === key;
  return { inspection: answer?.inspection ?? null, error: fresh ? answer.error : "", checking: key !== "" && !fresh };
}
