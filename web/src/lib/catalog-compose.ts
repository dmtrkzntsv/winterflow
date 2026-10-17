// The catalog detail shows a runnable Compose example. The x-winterflow
// extension is installation metadata and remains in the stored revision.
export function catalogComposeExample(content: string): string {
  const lines = content.split("\n");
  const visible: string[] = [];
  let inWinterflow = false;
  let removed = false;
  for (const line of lines) {
    if (/^x-winterflow\s*:/.test(line)) {
      inWinterflow = true;
      removed = true;
      continue;
    }
    if (inWinterflow && /^(?:[^\s#-][^:]*:|---\s*$)/.test(line)) inWinterflow = false;
    if (!inWinterflow) visible.push(line);
  }
  return removed ? visible.join("\n").trimEnd() + (content.endsWith("\n") ? "\n" : "") : content;
}
