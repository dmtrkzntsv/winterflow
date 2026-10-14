import { createElement } from "react";
import { Box } from "lucide-react";

import { cn } from "@/lib/utils";
import { getAppIcon } from "@/lib/app-icons";

// AppIcon renders an app's colored tile. When `icon` names a known icon it shows
// that glyph; otherwise it falls back to the uppercase initial of `name`. An
// app without a name (not reconciled yet) gets a generic box, never a
// character of its id.
export function AppIcon({
  name,
  icon,
  color,
  className,
}: {
  name?: string;
  icon?: string;
  color?: string;
  className?: string;
}) {
  const bg = color || "#64748b";
  const glyph = getAppIcon(icon);
  const initial = name?.trim()[0];
  return (
    <div
      className={cn(
        "flex items-center justify-center rounded-md font-medium text-white",
        className,
      )}
      style={{ backgroundColor: bg }}
    >
      {glyph ? (
        createElement(glyph, { className: "size-[60%]" })
      ) : initial ? (
        initial.toUpperCase()
      ) : (
        <Box className="size-[60%]" aria-hidden />
      )}
    </div>
  );
}
