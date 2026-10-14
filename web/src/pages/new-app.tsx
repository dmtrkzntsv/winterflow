import { useSearchParams } from "react-router-dom";

import { PageHeader } from "@/components/page-header";
import { ComposeFlow } from "@/components/new-app/compose-flow";
import { GitFlow } from "@/components/new-app/git-flow";
import { NewAppChoices } from "@/components/new-app/new-app-choices";
import { TemplateFlow } from "@/components/new-app/template-flow";
import { useServers } from "@/context/use-servers";
import { parseNewAppSource } from "@/lib/new-app";
import { appsPath, newAppPath, type NewAppSource } from "@/lib/routes";

const TITLES: Record<NewAppSource, string> = {
  template: "New app from a template",
  compose: "New app from a compose file",
  git: "New app from Git",
};

// NewAppPage is /server/:id/new: ?from=template|compose|git opens that
// form; anything else shows the ways to start (the same cards the Apps
// page's New app opens).
export default function NewAppPage() {
  const [params] = useSearchParams();
  const { activeServerId } = useServers();
  const serverId = activeServerId ?? "";
  const from = parseNewAppSource(params.get("from"));
  return (
    <div className="mx-auto w-full max-w-3xl space-y-6">
      <PageHeader
        title={from ? TITLES[from] : "New app"}
        subtitle={from ? undefined : "How do you want to start?"}
        back={from ? { to: newAppPath(serverId), label: "New app" } : { to: appsPath(serverId), label: "Apps" }}
      />
      {from === "template" ? (
        <TemplateFlow serverId={serverId} />
      ) : from === "compose" ? (
        <ComposeFlow serverId={serverId} />
      ) : from === "git" ? (
        <GitFlow serverId={serverId} />
      ) : (
        <NewAppChoices serverId={serverId} />
      )}
    </div>
  );
}
