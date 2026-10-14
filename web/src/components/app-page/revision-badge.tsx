import { StatusDot } from "@/components/status";

// RevisionBadge is what marks a version of an app: Live (it runs now) or a
// draft (saved, not deployed; draftLabel says how much to explain).
export function RevisionBadge({
  isDeployed,
  isUndeployedDraft,
  draftLabel = "Draft",
}: {
  isDeployed: boolean;
  isUndeployedDraft: boolean;
  draftLabel?: string;
}) {
  if (!isDeployed && !isUndeployedDraft) return null;
  return (
    <span className="inline-flex shrink-0 items-center gap-1.5 text-xs font-medium text-foreground">
      <StatusDot tone={isDeployed ? "ok" : "warn"} />
      {isDeployed ? "Live" : draftLabel}
    </span>
  );
}
