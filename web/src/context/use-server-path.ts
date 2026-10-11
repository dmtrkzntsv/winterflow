import { useCallback } from "react";

import { serverPath } from "@/lib/server-paths";

import { useServers } from "./use-servers";

// useServerPath returns a builder for links inside the active server's scope.
// Without an active server it yields the unscoped path, which the router
// redirects once a server is known.
export function useServerPath() {
  const { activeServerId } = useServers();
  return useCallback(
    (sub = "") => (activeServerId ? serverPath(activeServerId, sub) : sub || "/"),
    [activeServerId],
  );
}
