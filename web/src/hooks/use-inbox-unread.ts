import { useContext } from "react";

import { InboxUnreadContext, type InboxUnread } from "@/context/inbox-unread-context-base";

// Outside an InboxUnreadProvider nothing is unread.
const NONE: InboxUnread = { count: 0, seenAt: null, isUnread: () => false, markAllRead: () => {} };

// useInboxUnread is the shell's unread Inbox state (the sidebar badge, the
// Inbox page's dots and Mark all as read).
export function useInboxUnread(): InboxUnread {
  return useContext(InboxUnreadContext) ?? NONE;
}
