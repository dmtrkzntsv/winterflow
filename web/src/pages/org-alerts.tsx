import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";

import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ChannelsTab } from "@/components/alerts/channels-tab";
import { HistoryTab } from "@/components/alerts/history-tab";
import { RulesTab } from "@/components/alerts/rules-tab";

const TABS = ["history", "rules", "channels"] as const;
type Tab = (typeof TABS)[number];

// OrgAlertsPage is Org → Alerts: the alert history, the org-wide rules and
// the notification channels. The tab lives in ?tab= so links can open one.
export default function OrgAlertsPage() {
  const breadcrumbs = useMemo(() => [{ label: "Organization" }, { label: "Alerts" }], []);
  useAppBreadcrumbs(breadcrumbs);
  const [params, setParams] = useSearchParams();
  const tab: Tab = (TABS as readonly string[]).includes(params.get("tab") ?? "")
    ? (params.get("tab") as Tab)
    : TABS[0];

  return (
    <div className="space-y-4">
      <h1 className="text-2xl font-semibold tracking-tight">Alerts</h1>
      <Tabs value={tab} onValueChange={(v) => setParams({ tab: v }, { replace: true })}>
        <TabsList>
          <TabsTrigger value="history">History</TabsTrigger>
          <TabsTrigger value="rules">Rules</TabsTrigger>
          <TabsTrigger value="channels">Channels</TabsTrigger>
        </TabsList>
        <TabsContent value="history" className="mt-4">
          <HistoryTab />
        </TabsContent>
        <TabsContent value="rules" className="mt-4">
          <RulesTab />
        </TabsContent>
        <TabsContent value="channels" className="mt-4">
          <ChannelsTab />
        </TabsContent>
      </Tabs>
    </div>
  );
}
