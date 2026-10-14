import type { ReactNode } from "react";

import { NewAppOpenerContext } from "@/components/new-app/new-app-opener";
import { useNewAppDialog } from "@/components/new-app/use-new-app-dialog";
import { useServers } from "@/context/use-servers";

// NewAppDialogProvider holds the shell's New app chooser for the active
// server, like the Add server dialog: rendered beside the sidebar, opened
// through useNewAppOpener.
export function NewAppDialogProvider({ children }: { children: ReactNode }) {
  const { activeServerId } = useServers();
  const chooser = useNewAppDialog(activeServerId ?? "");
  return (
    <NewAppOpenerContext.Provider value={chooser.open}>
      {children}
      {chooser.dialog}
    </NewAppOpenerContext.Provider>
  );
}
