import { ChannelsTab } from "@/components/alerts/channels-tab";
import { RulesTab } from "@/components/alerts/rules-tab";
import { PageHeader } from "@/components/page-header";
import { Section } from "@/components/section";

// NotificationsSettingsPage is Settings → Notifications: where alerts are
// sent (channels) and when they fire (rules), on one page. Phase 5
// redesigns both; the existing components are mounted as they are.
export default function NotificationsSettingsPage() {
  return (
    <div className="space-y-8">
      <PageHeader title="Notifications" subtitle="Where alerts are sent, and when they fire." />
      <Section title="Channels">
        <ChannelsTab />
      </Section>
      <Section title="Alert rules">
        <RulesTab />
      </Section>
    </div>
  );
}
