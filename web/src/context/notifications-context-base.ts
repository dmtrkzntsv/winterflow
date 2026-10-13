import { createContext } from "react";

// Notification mirrors the backend model.Notification delivered over the SSE
// stream (/api/v1/sse).
export type Notification = {
  type: string;
  ref: string;
  // 0 = success, 1 = error (model.NotificationStatus).
  status?: number;
  payload?: unknown;
  error?: string;
  timestamp?: string;
};

// WAIT_TIMEOUT is the message of waitFor's rejection when no result came in
// time (the command may still have run).
export const WAIT_TIMEOUT = "Timed out waiting for command result";

export type NotificationHandler = (n: Notification) => void;

export type NotificationsContextValue = {
  connected: boolean;
  // subscribe registers a handler invoked for every incoming notification.
  // Returns an unsubscribe function.
  subscribe: (handler: NotificationHandler) => () => void;
  // waitFor resolves with the notification whose ref matches the given
  // request_id (an await-style bridge over the SSE stream). Rejects on timeout.
  waitFor: (requestId: string, timeoutMs?: number) => Promise<Notification>;
};

export const NotificationsContext =
  createContext<NotificationsContextValue | null>(null);
