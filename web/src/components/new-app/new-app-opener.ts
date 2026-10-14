import { createContext, useContext } from "react";

// NewAppOpenerContext carries the shell's New app chooser opener: the
// chooser lives above the sidebar, so it stays open when the phone's
// sidebar sheet (holding the button) closes.
export const NewAppOpenerContext = createContext<(() => void) | null>(null);

// useNewAppOpener opens the shell's New app chooser; null outside the
// shell.
export function useNewAppOpener(): (() => void) | null {
  return useContext(NewAppOpenerContext);
}
