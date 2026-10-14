import type { AppRevision } from "@/context/apps-context-base";

// What a version in an app's history reads as. The agent writes plain
// commit subjects ("save Shop", "update backups", "rename to Shop",
// "rollback to 1a2b3c4d", "update source to 5e6f7a8b"); people read what
// happened instead. The changed files aren't reported, so a save is just
// "Saved". A subject the UI doesn't know shows as it is.

// whenLabel writes a version's time for "Rolled back to …".
export function whenLabel(d: Date): string {
  return d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

// revisionLabel is the label of revisions[index]; revisions is the app's
// whole history, newest first (the oldest is the app's creation).
export function revisionLabel(
  revisions: readonly AppRevision[],
  index: number,
  formatWhen: (d: Date) => string = whenLabel,
): string {
  const rev = revisions[index];
  if (!rev) return "";
  if (index === revisions.length - 1) return "Created";
  const subject = rev.subject.trim();
  if (/^save\b/.test(subject)) return "Saved";
  if (subject === "update backups") return "Changed backups";
  const renamed = /^rename to (.+)$/.exec(subject);
  if (renamed) return `Renamed to ${renamed[1]}`;
  const rolledBack = /^rollback to ([0-9a-f]{4,40})$/i.exec(subject);
  if (rolledBack) {
    const short = rolledBack[1].toLowerCase();
    const to = revisions.find((r) => r.hash.toLowerCase().startsWith(short));
    return to ? `Rolled back to ${formatWhen(new Date(to.timestamp * 1000))}` : "Rolled back";
  }
  if (/^update source to \S+$/.test(subject)) return "Updated from its source";
  return rev.subject;
}
