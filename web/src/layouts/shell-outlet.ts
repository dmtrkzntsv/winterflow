import { useOutletContext } from "react-router-dom";

// ShellOutlet is what the app shell hands the page in its outlet: the one
// Add server dialog's opener (undefined on a standalone install, which has
// its one server).
export type ShellOutlet = { onAddServer?: () => void };

// useShellAddServer opens the shell's Add server dialog; undefined where
// adding a server isn't offered (standalone, or outside the shell).
export function useShellAddServer(): (() => void) | undefined {
  return useOutletContext<ShellOutlet | undefined>()?.onAddServer;
}
