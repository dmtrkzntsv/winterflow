import type { ReactNode } from "react";

import {
  NotificationsContext,
  type Notification,
  type NotificationHandler,
} from "@/context/notifications-context-base";

// fakeNotifications is an SSE stand-in: emit delivers a notification to
// every subscribed handler.
export function fakeNotifications() {
  const handlers = new Set<NotificationHandler>();
  const value = {
    connected: true,
    subscribe: (h: NotificationHandler) => {
      handlers.add(h);
      return () => {
        handlers.delete(h);
      };
    },
    waitFor: () => new Promise<Notification>(() => {}),
  };
  return {
    emit: (n: Notification) => handlers.forEach((h) => h(n)),
    Provider: ({ children }: { children: ReactNode }) => (
      <NotificationsContext.Provider value={value}>{children}</NotificationsContext.Provider>
    ),
  };
}
