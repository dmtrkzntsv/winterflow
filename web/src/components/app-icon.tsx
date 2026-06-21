import { cn } from "@/lib/utils";

// AppIcon renders an app's colored tile with its initial. (Lucide icon-name
// rendering can be added later; the initial avoids a dynamic-import dependency
// and is robust for any app.)
export function AppIcon({
  name,
  color,
  className,
}: {
  name?: string;
  color?: string;
  className?: string;
}) {
  const bg = color || "#64748b";
  const initial = (name?.trim()?.[0] ?? "?").toUpperCase();
  return (
    <div
      className={cn(
        "flex items-center justify-center rounded-md font-medium text-white",
        className,
      )}
      style={{ backgroundColor: bg }}
    >
      {initial}
    </div>
  );
}
