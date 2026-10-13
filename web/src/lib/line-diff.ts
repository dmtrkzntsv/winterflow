// A line diff for template revisions: a longest-common-subsequence diff of
// the lines between the common head and tail, which is all a template's
// files (at most 1 MiB, usually a few hundred lines) need. No dependency.

export type DiffLine = { op: " " | "+" | "-"; text: string };

// MAX_CELLS bounds the LCS table; past it the changed middle is shown as
// removed then added (still correct, just coarser).
const MAX_CELLS = 2_000_000;

export function diffLines(before: string, after: string): DiffLine[] {
  const a = before === "" ? [] : before.split("\n");
  const b = after === "" ? [] : after.split("\n");
  let head = 0;
  while (head < a.length && head < b.length && a[head] === b[head]) head++;
  let tail = 0;
  while (tail < a.length - head && tail < b.length - head && a[a.length - 1 - tail] === b[b.length - 1 - tail]) tail++;
  const midA = a.slice(head, a.length - tail);
  const midB = b.slice(head, b.length - tail);
  // A loop, not push(...lines): a spread of a huge file overflows the stack.
  const out: DiffLine[] = a.slice(0, head).map((text) => ({ op: " ", text }));
  for (const l of middle(midA, midB)) out.push(l);
  for (let i = a.length - tail; i < a.length; i++) out.push({ op: " ", text: a[i] });
  return out;
}

function middle(a: string[], b: string[]): DiffLine[] {
  const removed = a.map((text) => ({ op: "-" as const, text }));
  const added = b.map((text) => ({ op: "+" as const, text }));
  if (a.length === 0 || b.length === 0 || a.length * b.length > MAX_CELLS) return [...removed, ...added];
  // lcs[i][j] is the LCS length of a[i:] and b[j:].
  const w = b.length + 1;
  const lcs = new Uint32Array((a.length + 1) * w);
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      lcs[i * w + j] = a[i] === b[j] ? lcs[(i + 1) * w + j + 1] + 1 : Math.max(lcs[(i + 1) * w + j], lcs[i * w + j + 1]);
    }
  }
  const out: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      out.push({ op: " ", text: a[i] });
      i++;
      j++;
    } else if (lcs[(i + 1) * w + j] >= lcs[i * w + j + 1]) {
      out.push({ op: "-", text: a[i++] });
    } else {
      out.push({ op: "+", text: b[j++] });
    }
  }
  while (i < a.length) out.push({ op: "-", text: a[i++] });
  while (j < b.length) out.push({ op: "+", text: b[j++] });
  return out;
}

// Hunk is a run of changes with up to `context` unchanged lines around it;
// skipped counts the unchanged lines left out before it.
export type Hunk = { skipped: number; lines: DiffLine[] };

export function hunks(lines: DiffLine[], context = 3): Hunk[] {
  const keep = new Array<boolean>(lines.length).fill(false);
  lines.forEach((l, i) => {
    if (l.op === " ") return;
    for (let k = Math.max(0, i - context); k <= Math.min(lines.length - 1, i + context); k++) keep[k] = true;
  });
  const out: Hunk[] = [];
  let skipped = 0;
  let current: Hunk | null = null;
  lines.forEach((l, i) => {
    if (!keep[i]) {
      skipped++;
      current = null;
      return;
    }
    if (!current) {
      current = { skipped, lines: [] };
      out.push(current);
      skipped = 0;
    }
    current.lines.push(l);
  });
  return out;
}
