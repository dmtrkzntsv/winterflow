import { useCallback, useEffect, useState } from "react";

import { alertsGet } from "@/hooks/use-alerts";
import type { Channel } from "@/lib/alerts";

// useAlertChannels is the organization's notification channels, listed
// once for the Notifications page: the channel rows and the rules' "notify"
// lines read the same list, so a rename or delete shows in both at once.
export function useAlertChannels() {
  const [channels, setChannels] = useState<Channel[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      setChannels((await alertsGet<Channel[] | null>("get-channels")) ?? []);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't load the channels");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  return { channels, loading, error, refresh };
}
