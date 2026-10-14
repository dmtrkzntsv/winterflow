import { ChannelsSection } from "@/components/alerts/channels-section";
import { RulesSection } from "@/components/alerts/rules-section";
import { PageHeader } from "@/components/page-header";
import { useAlertChannels } from "@/hooks/use-alert-channels";

// NotificationsSettingsPage is Settings → Notifications: where alerts are
// sent (channels) and when they fire (rules), on one page. The channel list
// is loaded once here and shared, so a renamed or deleted channel shows in
// the rules at once.
export default function NotificationsSettingsPage() {
  const channels = useAlertChannels();
  return (
    <div className="space-y-8">
      <PageHeader title="Notifications" subtitle="Where alerts are sent, and when they fire." />
      <ChannelsSection
        channels={channels.channels}
        loading={channels.loading}
        error={channels.error}
        onChanged={channels.refresh}
      />
      <RulesSection channels={channels.channels} />
    </div>
  );
}
