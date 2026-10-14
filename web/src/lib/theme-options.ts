import { Monitor, Moon, Sun, type LucideIcon } from "lucide-react";

import type { ThemePreference } from "@/lib/theme";

// THEME_OPTIONS are the theme choices in the order they are offered; the
// ⌘K palette and the sidebar's user menu share them.
export const THEME_OPTIONS: { value: ThemePreference; label: string; icon: LucideIcon }[] = [
  { value: "light", label: "Light", icon: Sun },
  { value: "dark", label: "Dark", icon: Moon },
  { value: "system", label: "System", icon: Monitor },
];
