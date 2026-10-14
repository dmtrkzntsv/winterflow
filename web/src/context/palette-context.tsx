import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";

import {
  CommandPaletteContext,
  type CommandPaletteContextValue,
  type PaletteActionGroup,
} from "@/context/palette-context-base";
import { isPaletteShortcut } from "@/lib/keyboard";

// CommandPaletteProvider owns the palette's open state (⌘K / Ctrl+K
// toggles it anywhere) and the contextual actions pages register.
export function CommandPaletteProvider({ children }: { children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const [registry, setRegistry] = useState<ReadonlyMap<string, PaletteActionGroup>>(() => new Map());

  const register = useCallback((key: string, group: PaletteActionGroup) => {
    setRegistry((prev) => new Map(prev).set(key, group));
    return () =>
      setRegistry((prev) => {
        if (!prev.has(key)) return prev;
        const next = new Map(prev);
        next.delete(key);
        return next;
      });
  }, []);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (!isPaletteShortcut(event)) return;
      event.preventDefault();
      setOpen((o) => !o);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  const openPalette = useCallback(() => setOpen(true), []);
  const groups = useMemo(() => [...registry.values()], [registry]);
  const value = useMemo<CommandPaletteContextValue>(
    () => ({ open, setOpen, openPalette, groups, register }),
    [open, openPalette, groups, register],
  );
  return <CommandPaletteContext.Provider value={value}>{children}</CommandPaletteContext.Provider>;
}
