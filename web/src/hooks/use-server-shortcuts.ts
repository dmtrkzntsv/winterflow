import { useEffect } from "react";

import { useServers } from "@/context/use-servers";
import { serverShortcutIndex } from "@/lib/keyboard";

// useServerShortcuts wires ⌘1…⌘9 (Ctrl elsewhere) to the switcher's
// servers (selectServer decides where the tab lands). Keys past the last
// server stay the browser's. Off on a standalone install (one server).
export function useServerShortcuts(enabled: boolean): void {
  const { servers, selectServer } = useServers();
  useEffect(() => {
    if (!enabled) return;
    const onKeyDown = (event: KeyboardEvent) => {
      const index = serverShortcutIndex(event);
      if (index === null || index >= servers.length) return;
      event.preventDefault();
      selectServer(servers[index].id);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [enabled, servers, selectServer]);
}
