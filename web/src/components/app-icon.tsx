import { createElement } from "react";

import { cn } from "@/lib/utils";
import { getAppIcon } from "@/lib/app-icons";

// AppIcon renders an app's colored tile. When `icon` names a known icon it shows
// that glyph; otherwise it falls back to the uppercase initial of `name`, or
// of the app `id` for an app not reconciled yet (no name), never a bare "?"
// when either is known.
export function AppIcon({
  name,
  id,
  icon,
  color,
  className,
}: {
  name?: string;
  id?: string;
  icon?: string;
  color?: string;
  className?: string;
}) {
  const bg = color || "#64748b";
  const glyph = getAppIcon(icon);
  return (
    <div
      className={cn(
        "flex items-center justify-center rounded-md font-medium text-white",
        className,
      )}
      style={{ backgroundColor: bg }}
    >
      {glyph
        ? createElement(glyph, { className: "size-[60%]" })
        : ((name?.trim() || id)?.[0] ?? "?").toUpperCase()}
    </div>
  );
}
