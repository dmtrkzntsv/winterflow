import { Link } from "react-router-dom";
import { ArrowRight } from "lucide-react";

import { HistoryTab } from "@/components/alerts/history-tab";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { useInboxUnread } from "@/hooks/use-inbox-unread";
import { settingsPath } from "@/lib/routes";

// InboxPage is /inbox, org-wide: for now the alert history as it was on
// Org → Alerts (phase 4 redesigns it) with Mark all as read, which clears
// the sidebar badge.
export default function InboxPage() {
  const { count, markAllRead } = useInboxUnread();
  return (
    <div className="space-y-6">
      <PageHeader
        title="Inbox"
        subtitle="Alerts from every server."
        actions={
          <>
            {count > 0 ? (
              <Button variant="outline" size="sm" onClick={markAllRead}>
                Mark all as read
              </Button>
            ) : null}
            <Button asChild variant="ghost" size="sm">
              <Link to={settingsPath("notifications")}>
                Notification settings
                <ArrowRight className="size-4" aria-hidden />
              </Link>
            </Button>
          </>
        }
      />
      <HistoryTab />
    </div>
  );
}
