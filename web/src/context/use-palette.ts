import { useContext, useEffect, useId, useRef } from "react";

import {
  CommandPaletteContext,
  type CommandPaletteContextValue,
  type PaletteAction,
} from "@/context/palette-context-base";

const INERT: CommandPaletteContextValue = {
  open: false,
  setOpen: () => {},
  openPalette: () => {},
  groups: [],
  register: () => () => {},
};

// useCommandPalette is the palette's state; outside the shell (component
// and page tests) an inert value, so nothing needs a provider.
export function useCommandPalette(): CommandPaletteContextValue {
  return useContext(CommandPaletteContext) ?? INERT;
}

// usePaletteActions offers actions in the palette while the calling page
// is mounted. Pass a fresh array every render if convenient: the group is
// re-registered only when the heading or an action's id, label or disabled
// state changes, and every run calls the newest handler.
export function usePaletteActions(heading: string, actions: PaletteAction[]): void {
  const register = useContext(CommandPaletteContext)?.register;
  const key = useId();
  const latest = useRef(actions);
  useEffect(() => {
    latest.current = actions;
  });
  const signature = actions.map((a) => `${a.id}\u0000${a.label}\u0000${a.disabled ? 1 : 0}`).join("\u0001");

  useEffect(() => {
    if (!register) return;
    return register(key, {
      heading,
      actions: latest.current.map((action) => ({
        ...action,
        run: () => latest.current.find((a) => a.id === action.id)?.run(),
      })),
    });
  }, [register, key, heading, signature]);
}
