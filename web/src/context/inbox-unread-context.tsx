import type { ReactNode } from "react";

import { InboxUnreadContext } from "@/context/inbox-unread-context-base";
import { useInboxUnreadState } from "@/hooks/use-inbox-unread-state";

// InboxUnreadProvider holds the unread Inbox state once for the whole shell:
// the sidebar badge and the Inbox page read the same instance, so an alert
// costs one read of the newest events, not one per consumer.
export function InboxUnreadProvider({ children }: { children: ReactNode }) {
  const value = useInboxUnreadState();
  return <InboxUnreadContext.Provider value={value}>{children}</InboxUnreadContext.Provider>;
}
