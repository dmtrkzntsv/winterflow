import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { FileCode, GitBranch, LayoutTemplate, type LucideIcon } from "lucide-react";

import { useTemplatesApi } from "@/hooks/use-templates-api";
import { newAppPath, type NewAppSource } from "@/lib/routes";

const CHOICES: readonly { from: NewAppSource; title: string; text: string; icon: LucideIcon }[] = [
  { from: "template", title: "From a template", text: "Fill in a few values. The template brings the rest.", icon: LayoutTemplate },
  { from: "compose", title: "Compose file", text: "Write or paste a Docker Compose file.", icon: FileCode },
  { from: "git", title: "Git repository", text: "Run a repository's compose file and redeploy on new commits.", icon: GitBranch },
];

// NewAppChoices are the ways to start a new app, as link cards. "From a
// template" shows only when the organization has a template (a failed read
// counts as none).
export function NewAppChoices({ serverId, onPick }: { serverId: string; onPick?: () => void }) {
  const api = useTemplatesApi();
  const [templates, setTemplates] = useState(0);
  useEffect(() => {
    let live = true;
    api.listTemplates().then(
      (list) => live && setTemplates(list.length),
      () => live && setTemplates(0),
    );
    return () => {
      live = false;
    };
  }, [api]);

  const shown = CHOICES.filter((c) => c.from !== "template" || templates > 0);
  return (
    <ul aria-label="Ways to start" className="grid gap-3 sm:grid-cols-[repeat(auto-fit,minmax(12rem,1fr))]">
      {shown.map((c) => (
        <li key={c.from}>
          <Link
            to={newAppPath(serverId, c.from)}
            onClick={onPick}
            className="flex h-full min-h-24 flex-col gap-2 rounded-xl border bg-card p-4 transition-colors duration-150 hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none motion-reduce:transition-none"
          >
            <c.icon className="size-5 text-primary" aria-hidden />
            <span className="font-medium">{c.title}</span>
            <span className="text-sm text-muted-foreground">{c.text}</span>
          </Link>
        </li>
      ))}
    </ul>
  );
}
