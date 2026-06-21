import { createContext } from "react";

// Notification mirrors the backend model.Notification delivered over the SSE
// stream (/api/v1/notification/stream).
export type Notification = {
  type: string;
  ref: string;
  // 0 = success, 1 = error (model.NotificationStatus).
  status?: number;
  payload?: unknown;
  error?: string;
  timestamp?: string;
};

export type NotificationHandler = (n: Notification) => void;

export type NotificationsContextValue = {
  connected: boolean;
  // subscribe registers a handler invoked for every incoming notification.
  // Returns an unsubscribe function.
  subscribe: (handler: NotificationHandler) => () => void;
};

export const NotificationsContext =
  createContext<NotificationsContextValue | null>(null);
