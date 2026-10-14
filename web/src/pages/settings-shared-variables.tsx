import { useMemo } from "react";

import { PageHeader } from "@/components/page-header";
import { Section } from "@/components/section";
import { useServers } from "@/context/use-servers";
import { createSharedVariablesApi, type SharedVariablesApi } from "@/lib/shared-variables";
import OrgVariablesPage from "@/pages/org-variables";
import { VariablesCard } from "@/pages/settings-variables";

// SharedVariablesSettingsPage is Settings → Shared variables: the
// organization's variables, then the active server's own (formerly the
// server settings' Variables tab). Phase 5 merges them into one list.
export default function SharedVariablesSettingsPage({ api: injected }: { api?: SharedVariablesApi }) {
  const api = useMemo(() => injected ?? createSharedVariablesApi(), [injected]);
  const { activeServer } = useServers();
  return (
    <div className="space-y-8">
      <PageHeader title="Shared variables" subtitle={"Values every app can use as ${NAME}."} />
      <OrgVariablesPage api={api} />
      {activeServer ? (
        <Section title={`${activeServer.name} only`}>
          <VariablesCard key={activeServer.id} serverId={activeServer.id} api={api} />
        </Section>
      ) : null}
    </div>
  );
}
