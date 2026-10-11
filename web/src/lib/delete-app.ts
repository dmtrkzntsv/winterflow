import type { DeleteAppResult } from "@/context/apps-context-base";

// DeleteChoice is what to delete with the app.
export type DeleteChoice = { withVolumes: boolean; withBackups: boolean };

// DeleteOutcome is the toast a finished delete shows.
export type DeleteOutcome = { level: "success" | "warning"; title: string; description?: string };

// deleteOutcome reads a delete's result. An agent that predates volume or
// backup deletion ignores the flag (it reports false): the user must not be
// told the data is gone.
export function deleteOutcome(choice: DeleteChoice, res: DeleteAppResult): DeleteOutcome {
  const kept: string[] = [];
  if (choice.withVolumes && !res.volumesDeleted) kept.push("volumes");
  if (choice.withBackups && !res.backupsDeleted) kept.push("backups");
  if (kept.length > 0) {
    return {
      level: "warning",
      title: `App deleted, but its ${kept.join(" and ")} were kept`,
      // A current agent attaches the reason as a warning; only a silent
      // false means an agent that predates the flag.
      description: res.warnings.length
        ? res.warnings.join("\n")
        : "This server's agent is too old to delete them. Update the agent, then remove them in Server Settings.",
    };
  }
  if (res.warnings.length) {
    return { level: "warning", title: "App deleted with cleanup warnings", description: res.warnings.join("\n") };
  }
  const also = [choice.withVolumes && "volumes", choice.withBackups && "backups"].filter(Boolean);
  // "App deleted", "App and volumes deleted", "App, volumes and backups deleted".
  const items = ["app", ...also] as string[];
  const last = items.pop();
  const named = items.length ? `${items.join(", ")} and ${last}` : last;
  return { level: "success", title: `${named![0].toUpperCase()}${named!.slice(1)} deleted` };
}
