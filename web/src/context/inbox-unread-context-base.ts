import { createContext } from "react";

export type InboxUnread = {
  count: number;
  // seenAt is the user's last-seen time (ms), null before it is recorded.
  seenAt: number | null;
  isUnread: (createdAt: string) => boolean;
  // markAllRead moves last-seen to now, or past the newest event when the
  // server's clock is ahead of this browser's.
  markAllRead: () => void;
};

export const InboxUnreadContext = createContext<InboxUnread | null>(null);
