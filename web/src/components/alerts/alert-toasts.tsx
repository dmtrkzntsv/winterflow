import { useEffect } from "react";
import { toast } from "sonner";

import { useNotifications } from "@/context/use-notifications";
import { ALERT_NOTIFICATION, type AlertNotice } from "@/lib/alerts";

// AlertToasts shows a toast for every alert transition pushed over SSE
// while the app is open: red when one fires, green when it resolves.
export function AlertToasts() {
  const { subscribe } = useNotifications();
  useEffect(
    () =>
      subscribe((n) => {
        if (n.type !== ALERT_NOTIFICATION || !n.payload) return;
        const notice = n.payload as AlertNotice;
        if (notice.kind === "fired") toast.error(notice.title);
        else toast.success(notice.title);
      }),
    [subscribe],
  );
  return null;
}
