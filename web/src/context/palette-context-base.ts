import { createContext } from "react";
import type { LucideIcon } from "lucide-react";

// PaletteAction is a command a page offers in the ⌘K palette while it is
// open (an app page's Restart, Stop, …).
export type PaletteAction = {
  id: string;
  label: string;
  icon?: LucideIcon;
  keywords?: string[];
  disabled?: boolean;
  run: () => void;
};

// PaletteActionGroup is one page's actions under a heading (the app's name).
export type PaletteActionGroup = { heading: string; actions: PaletteAction[] };

export type CommandPaletteContextValue = {
  open: boolean;
  setOpen: (open: boolean) => void;
  openPalette: () => void;
  // groups are the registered contextual actions, in registration order.
  groups: PaletteActionGroup[];
  // register adds a group under key and returns its removal.
  register: (key: string, group: PaletteActionGroup) => () => void;
};

export const CommandPaletteContext = createContext<CommandPaletteContextValue | null>(null);
